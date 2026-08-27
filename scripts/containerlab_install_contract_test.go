package main

import (
	"errors"
	"os"
	"os/exec" //nolint:depguard // Execution is required to validate the remaining Python vendor bootstrap boundary.
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerlabBootstrapSecurityBoundaries(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	bootstrap := filepath.Join(repositoryRoot, "test", "interop-clab", "vendor_images.py")
	fakeBin := t.TempDir()
	fakePodman := filepath.Join(fakeBin, "podman")
	if err := os.WriteFile(fakePodman, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}

	contract := `
import importlib.util
import pathlib
import sys

bootstrap_path = pathlib.Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("gobfd_interop_clab_bootstrap", bootstrap_path)
if spec is None or spec.loader is None:
    raise RuntimeError("load bootstrap module spec")
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)

valid_url = "https://api.github.com/repos/vyos/vyos-rolling-nightly-builds/releases/latest"
assert module._validated_https_url(valid_url) == valid_url
for invalid_url in (
    "http://api.github.com/repos/vyos/vyos-rolling-nightly-builds/releases/latest",
    "file:///etc/passwd",
    "https://example.com/vyos.iso",
    "https://user@github.com/vyos.iso",
    "https://github.com:444/vyos.iso",
):
    try:
        module._validated_https_url(invalid_url)
    except ValueError:
        pass
    else:
        raise AssertionError(f"accepted non-allowlisted URL: {invalid_url}")

redirect_handler = module._AllowlistedRedirectHandler()
original_request = module.Request(valid_url)
allowed_redirect = "https://github.com/vyos/vyos-rolling-nightly-builds/releases/download/test.iso"
redirect_request = redirect_handler.redirect_request(
    original_request,
    None,
    302,
    "Found",
    {},
    allowed_redirect,
)
assert redirect_request is not None
assert redirect_request.full_url == allowed_redirect
for invalid_redirect in (
    "http://github.com/vyos.iso",
    "https://example.com/vyos.iso",
):
    try:
        redirect_handler.redirect_request(
            original_request,
            None,
            302,
            "Found",
            {},
            invalid_redirect,
        )
    except ValueError:
        pass
    else:
        raise AssertionError(f"accepted non-allowlisted redirect: {invalid_redirect}")

want_podman = str(pathlib.Path(sys.argv[2], "podman").absolute())
assert module._resolve_executable("podman") == want_podman
for invalid_executable in ("sh", "../podman", "/tmp/podman"):
    try:
        module._resolve_executable(invalid_executable)
    except ValueError:
        pass
    else:
        raise AssertionError(f"accepted non-allowlisted executable: {invalid_executable}")
`

	command := exec.CommandContext(
		t.Context(),
		"uv",
		"run",
		"--frozen",
		"--no-default-groups",
		"--",
		"python",
		"-c",
		contract,
		bootstrap,
		fakeBin,
	)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap security contract: %v\n%s", err, output)
	}
}

func TestContainerlabBootstrapOwnedOrchestrationUsesGo(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, path := range []string{
		filepath.Join(repositoryRoot, "test", "internal", "clabbootstrap", "orchestration.go"),
		filepath.Join(repositoryRoot, "test", "cmd", "clabbootstrap", "main.go"),
		filepath.Join(repositoryRoot, "test", "interop-clab", "vendor_images.py"),
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("required bootstrap split path %s: %v", path, statErr)
			continue
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			t.Errorf("bootstrap split path %s is not a nonempty regular file", path)
		}
	}
	legacy := filepath.Join(repositoryRoot, "test", "interop-clab", "bootstrap.py")
	if _, statErr := os.Lstat(legacy); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("legacy Python bootstrap still exists: %v", statErr)
	}

	vendor := readContractFile(t, "../test/interop-clab/vendor_images.py")
	for _, forbidden := range []string{
		"ThreadPoolExecutor",
		"_pull_images",
		"_build_gobfd",
		"_print_inventory",
		"_run_deploy_or_test",
		"--deploy",
		"--test",
		"--jobs",
	} {
		if strings.Contains(vendor, forbidden) {
			t.Errorf("vendor Python helper retains Go-owned orchestration %q", forbidden)
		}
	}
	for _, required := range []string{"vyos", "arista", "cisco", "_validated_https_url"} {
		if !strings.Contains(vendor, required) {
			t.Errorf("vendor Python helper is missing %q", required)
		}
	}

	makefile := readContractFile(t, "../Makefile")
	for _, required := range []string{
		"interop-clab-bootstrap:",
		"go run ./test/cmd/clabbootstrap",
		"PYTHON_FILES := test/interop-clab/vendor_images.py",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile lacks bootstrap split contract %q", required)
		}
	}
	if strings.Contains(makefile, "test/interop-clab/bootstrap.py") {
		t.Error("Makefile still references the legacy Python bootstrap")
	}
}

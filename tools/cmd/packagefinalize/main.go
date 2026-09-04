// Command packagefinalize replaces nFPM RPMs with native lifecycle programs
// and refreshes the GoReleaser checksum manifest before release upload.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/rpmpack"
)

const (
	stableVersionParts    = 3
	maxRPMTime            = int64(1<<32 - 1)
	postInstallProgramTag = 1086
	preRemoveProgramTag   = 1087
	postInstallProgram    = "/usr/libexec/gobfd-postinstall"
	preRemoveProgram      = "/usr/libexec/gobfd-preremove"
	amd64                 = "amd64"
	arm64                 = "arm64"
)

var errInvalidPackageInput = errors.New("invalid release package input")

func main() {
	root, err := filepath.Abs("..")
	if err == nil {
		err = run(root, os.Getenv("GITHUB_REF_NAME"))
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "finalize release packages: %v\n", err)
		os.Exit(1)
	}
}

func run(root, refName string) error {
	version, err := stableVersion(refName)
	if err != nil {
		return err
	}
	for _, arch := range []string{amd64, arm64} {
		path := filepath.Join(root, "dist", "gobfd_"+version+"_linux_"+arch+".rpm")
		if err := replaceRPM(path, root, version, arch); err != nil {
			return fmt.Errorf("replace %s RPM: %w", arch, err)
		}
	}
	if err := refreshArtifactManifest(filepath.Join(root, "dist"), version); err != nil {
		return fmt.Errorf("refresh GoReleaser artifact manifest: %w", err)
	}
	if err := refreshChecksums(filepath.Join(root, "dist")); err != nil {
		return fmt.Errorf("refresh release checksums: %w", err)
	}
	return nil
}

func stableVersion(refName string) (string, error) {
	version, found := strings.CutPrefix(refName, "v")
	if !found {
		return "", fmt.Errorf("%w: release ref %q lacks v prefix", errInvalidPackageInput, refName)
	}
	parts := strings.Split(version, ".")
	if len(parts) != stableVersionParts {
		return "", fmt.Errorf("%w: release ref %q is not stable SemVer", errInvalidPackageInput, refName)
	}
	for _, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != part {
			return "", fmt.Errorf("%w: release ref %q is not canonical stable SemVer", errInvalidPackageInput, refName)
		}
	}
	return version, nil
}

func replaceRPM(path, root, version, arch string) (returnErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gobfd-rpm-*")
	if err != nil {
		return fmt.Errorf("create temporary RPM: %w", err)
	}
	temporaryName := temporary.Name()
	open := true
	defer func() {
		if open {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		if returnErr != nil {
			// #nosec G703 -- CreateTemp returned this exact owned path.
			returnErr = errors.Join(returnErr, os.Remove(temporaryName))
		}
	}()
	if err := writeRPM(temporary, root, version, arch); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary RPM: %w", err)
	}
	open = false
	// #nosec G703 -- source is CreateTemp-owned; destination is a fixed release artifact path.
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish RPM: %w", err)
	}
	return nil
}

func writeRPM(output io.Writer, root, version, arch string) error {
	if arch != amd64 && arch != arm64 {
		return fmt.Errorf("%w: unsupported RPM architecture %q", errInvalidPackageInput, arch)
	}
	rpmArch := "x86_64"
	if arch == arm64 {
		rpmArch = "aarch64"
	}
	target := "linux_" + arch + "_v1"
	if arch == arm64 {
		target = "linux_arm64_v8.0"
	}
	requires := rpmpack.Relations{}
	if err := requires.Set("systemd"); err != nil {
		return fmt.Errorf("set RPM dependency: %w", err)
	}
	description := "BFD protocol daemon implementing RFC 5880/5881 for network failure detection."
	buildHost, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("resolve RPM build host: %w", err)
	}
	packageRPM, err := rpmpack.NewRPM(rpmpack.RPMMetaData{
		Name: "gobfd", Summary: description, Description: description,
		Version: version, Release: "1", Arch: rpmArch, OS: "linux", Vendor: "dantte-lp",
		URL: "https://github.com/dantte-lp/gobfd", Packager: "dantte-lp", Licence: "Apache-2.0",
		BuildHost: buildHost, BuildTime: time.Now(), Requires: requires,
	})
	if err != nil {
		return fmt.Errorf("create RPM: %w", err)
	}
	if err := addRPMContents(packageRPM, root, target); err != nil {
		return err
	}
	packageRPM.AddPostin("\n")
	packageRPM.AddPreun("\n")
	packageRPM.AddCustomTag(postInstallProgramTag, rpmpack.EntryString(postInstallProgram))
	packageRPM.AddCustomTag(preRemoveProgramTag, rpmpack.EntryString(preRemoveProgram))
	if err := packageRPM.Write(output); err != nil {
		return fmt.Errorf("write RPM: %w", err)
	}
	return nil
}

func addRPMContents(packageRPM *rpmpack.RPM, root, target string) error {
	publicBinaries := [...]string{"gobfd", "gobfdctl", "gobfd-haproxy-agent", "gobfd-exabgp-bridge"}
	for _, binary := range publicBinaries {
		if err := addRPMFile(packageRPM, filepath.Join(root, "dist", binary+"_"+target, binary),
			"/usr/local/bin/"+binary, 0o755, rpmpack.GenericFile); err != nil {
			return err
		}
	}
	helper := filepath.Join(root, "dist", "gobfd-package-lifecycle_"+target, "gobfd-package-lifecycle")
	for _, destination := range []string{postInstallProgram, preRemoveProgram} {
		if err := addRPMFile(packageRPM, helper, destination, 0o755, rpmpack.GenericFile); err != nil {
			return err
		}
	}
	files := []struct {
		source      string
		destination string
		mode        uint
		kind        rpmpack.FileType
	}{
		{"configs/gobfd.example.yml", "/etc/gobfd/gobfd.yml", 0o644, rpmpack.ConfigFile | rpmpack.NoReplaceFile},
		{"deployments/systemd/gobfd.service", "/usr/lib/systemd/system/gobfd.service", 0o644, rpmpack.GenericFile},
		{"deployments/systemd/gobfd.sysusers", "/usr/lib/sysusers.d/gobfd.conf", 0o644, rpmpack.GenericFile},
		{"deployments/systemd/gobfd.tmpfiles", "/usr/lib/tmpfiles.d/gobfd.conf", 0o644, rpmpack.GenericFile},
	}
	for _, file := range files {
		if err := addRPMFile(
			packageRPM, filepath.Join(root, file.source), file.destination, file.mode, file.kind,
		); err != nil {
			return err
		}
	}
	return nil
}

func addRPMFile(rpm *rpmpack.RPM, source, destination string, mode uint, kind rpmpack.FileType) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read RPM input %s: %w", source, err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat RPM input %s: %w", source, err)
	}
	timestamp := info.ModTime().Unix()
	if timestamp < 0 || timestamp > maxRPMTime {
		return fmt.Errorf("%w: RPM input %s modification time is out of range", errInvalidPackageInput, source)
	}
	rpm.AddFile(rpmpack.RPMFile{
		Name: destination, Body: data, Mode: mode, MTime: uint32(timestamp),
		Owner: "root", Group: "root", Type: kind,
	})
	return nil
}

func refreshArtifactManifest(dist, version string) error {
	manifestPath := filepath.Join(dist, "artifacts.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read artifact manifest: %w", err)
	}
	var artifacts []map[string]json.RawMessage
	if decodeErr := json.Unmarshal(manifest, &artifacts); decodeErr != nil {
		return fmt.Errorf("decode artifact manifest: %w", decodeErr)
	}
	want, digestErr := rpmArtifactDigests(dist, version)
	if digestErr != nil {
		return digestErr
	}
	for index := range artifacts {
		var path string
		if decodeErr := json.Unmarshal(artifacts[index]["path"], &path); decodeErr != nil {
			return fmt.Errorf("decode artifact path: %w", decodeErr)
		}
		digest, found := want[path]
		if !found {
			continue
		}
		var extra map[string]json.RawMessage
		if decodeErr := json.Unmarshal(artifacts[index]["extra"], &extra); decodeErr != nil {
			return fmt.Errorf("decode artifact metadata for %s: %w", path, decodeErr)
		}
		encoded, encodeErr := json.Marshal(digest)
		if encodeErr != nil {
			return fmt.Errorf("encode finalized RPM checksum: %w", encodeErr)
		}
		extra["Checksum"] = encoded
		artifacts[index]["extra"], encodeErr = json.Marshal(extra)
		if encodeErr != nil {
			return fmt.Errorf("encode artifact metadata for %s: %w", path, encodeErr)
		}
		delete(want, path)
	}
	if len(want) != 0 {
		return fmt.Errorf("%w: artifact manifest lacks finalized RPMs", errInvalidPackageInput)
	}
	updated, err := json.MarshalIndent(artifacts, "", "  ")
	if err != nil {
		return fmt.Errorf("encode artifact manifest: %w", err)
	}
	updated = append(updated, '\n')
	// #nosec G306 -- the release artifact manifest is intentionally world-readable.
	if err := os.WriteFile(manifestPath, updated, 0o644); err != nil {
		return fmt.Errorf("write artifact manifest: %w", err)
	}
	return nil
}

func rpmArtifactDigests(dist, version string) (map[string]string, error) {
	digests := make(map[string]string, 2)
	for _, arch := range []string{amd64, arm64} {
		name := "gobfd_" + version + "_linux_" + arch + ".rpm"
		// #nosec G703 -- name contains only the validated stable version and fixed architectures.
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			return nil, fmt.Errorf("read finalized RPM %s: %w", arch, err)
		}
		digests[filepath.Join("dist", name)] = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	}
	return digests, nil
}

func refreshChecksums(dist string) error {
	manifestPath := filepath.Join(dist, "checksums.txt")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read checksum manifest: %w", err)
	}
	names := make([]string, 0, 8)
	lines := strings.Split(string(manifest), "\n")
	if len(lines) < 2 || lines[len(lines)-1] != "" {
		return fmt.Errorf("%w: checksum manifest is not newline terminated", errInvalidPackageInput)
	}
	for _, line := range lines[:len(lines)-1] {
		_, name, found := strings.Cut(line, "  ")
		if !found || filepath.Base(name) != name {
			return fmt.Errorf("%w: invalid checksum record %q", errInvalidPackageInput, line)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return fmt.Errorf("%w: checksum manifest is empty", errInvalidPackageInput)
	}
	slices.Sort(names)
	var rendered strings.Builder
	for _, name := range names {
		// #nosec G703 -- name is restricted to its canonical basename above.
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			return fmt.Errorf("read checksummed artifact %s: %w", name, err)
		}
		digest := sha256.Sum256(data)
		_, _ = fmt.Fprintf(&rendered, "%x  %s\n", digest, name)
	}
	// #nosec G306 -- published checksum manifests are intentionally world-readable.
	if err := os.WriteFile(manifestPath, []byte(rendered.String()), 0o644); err != nil {
		return fmt.Errorf("write checksum manifest: %w", err)
	}
	return nil
}

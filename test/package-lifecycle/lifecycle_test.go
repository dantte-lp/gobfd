//go:build package_lifecycle_testcontainers

// Package packagelifecycle_test installs the release DEB into
// systemd-enabled Debian 13 containers and proves the
// hardened gobfd.service contract (gobfd-qj0.8.2.3.3).
package packagelifecycle_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

// distEnv names the directory holding the gobfd_<version>_linux_<arch> packages.
const distEnv = "GOBFD_PACKAGE_DIST"

// artifactEnv optionally names reports/e2e/package-lifecycle for the command log.
const artifactEnv = "GOBFD_PACKAGE_ARTIFACT_DIR"

const (
	containerMemory = 1 << 30
	containerPIDs   = 1024
	packageTestKind = "package-lifecycle"
	// The hardened unit sets WatchdogSec=30s; the hold must outlast it.
	watchdogHold = 35 * time.Second
	pollStep     = 500 * time.Millisecond
)

// sandboxDenials matches the errno text of a refused address family or system call.
var sandboxDenials = regexp.MustCompile(`(?i)operation not permitted|address family not supported|bad system call`)

type distro struct {
	name           string
	containerfile  string
	installSystemd string
	packageSuffix  string
	install        func(path string) []string
	remove         []string
}

func distros() []distro {
	return []distro{
		{
			name:          "debian-13",
			containerfile: "deployments/docker/Containerfile.release",
			installSystemd: "apt-get update && apt-get install -y --no-install-recommends " +
				"systemd systemd-sysv iproute2 procps && rm -rf /var/lib/apt/lists/*",
			packageSuffix: ".deb",
			install:       func(path string) []string { return []string{"dpkg", "-i", path} },
			remove:        []string{"dpkg", "--remove", "gobfd"},
		},
	}
}

func TestPackageLifecycle(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	dist := os.Getenv(distEnv)
	if dist == "" {
		t.Fatalf("%s must name the directory with the built DEB", distEnv)
	}
	root := repositoryRoot(t)
	runID := time.Now().UTC().Format("20060102T150405Z")

	for _, d := range distros() {
		var image, imageID, containerID string
		t.Run(d.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
			defer cancel()

			pkg := findPackage(t, dist, d.packageSuffix)
			image, imageID = buildSystemdImage(ctx, t, endpoint, root, d, runID)
			ctr := startSystemd(ctx, t, image, filepath.Dir(pkg))
			containerID = ctr.GetContainerID()
			h := &host{t: t, ctx: ctx, ctr: ctr, evidence: &strings.Builder{}}
			t.Cleanup(func() { h.writeEvidence(runID, d.name) })
			t.Cleanup(h.dumpOnFailure)

			h.mustRun(d.install("/packages/" + filepath.Base(pkg))...)
			assertInstalledUnit(h, root)
			if directory := strings.TrimSpace(h.mustRun("stat", "-c", "%a %U:%G", "/etc/gobfd")); directory != "750 root:gobfd" {
				t.Fatalf("configuration directory = %q, want 750 root:gobfd", directory)
			}
			h.mustRun("getent", "passwd", "gobfd")
			h.mustRun("getent", "group", "gobfd-admin")
			if d.packageSuffix == ".deb" &&
				!strings.Contains(h.mustRun("dpkg-query", "-W", "-f=${Depends}", "gobfd"), "procps") {
				t.Error("DEB does not depend on procps, which provides ExecReload's /bin/kill")
			}
			for _, argv := range topology() {
				h.mustRun(argv...)
			}
			h.copy(configA(true), "/etc/gobfd/gobfd.yml")
			h.copy(configB(), "/etc/gobfd-peer.yml")
			h.mustRun("systemd-run", "--unit=gobfd-peer", "nsenter", "--net="+peerNetns,
				"/usr/local/bin/gobfd", "-config", "/etc/gobfd-peer.yml")

			h.mustRun("systemctl", "start", "gobfd")
			pid := h.mainPID()
			assertSandbox(h, pid)
			h.waitUp([]string{"single-hop", "multi-hop", "micro-bfd", "vxlan", "geneve"}, 7)
			h.mustRun("gobfdctl", "--format", "table", "session", "list")

			sleepContext(ctx, t, watchdogHold)
			h.expectShow("ActiveState", "active")
			h.expectShow("NRestarts", "0")
			for _, state := range []string{"disabled", "enabled"} {
				action := "disable"
				if state == "enabled" {
					action = "enable"
				}
				h.mustRun("systemctl", action, "gobfd")
				h.upgradePackage("/packages/"+filepath.Base(pkg), state)
				h.waitUp([]string{"single-hop", "multi-hop", "micro-bfd", "vxlan", "geneve"}, 7)
			}

			h.copy(configA(false), "/etc/gobfd/gobfd.yml")
			h.mustRun("systemctl", "reload", "gobfd")
			h.waitSessions(func(out string) bool { return !strings.Contains(out, "fd00:10::2") })
			if got := h.mainPID(); got != pid {
				t.Fatalf("MainPID after reload = %d, want unchanged %d", got, pid)
			}
			h.expectShow("NRestarts", "0")

			h.mustRun("systemctl", "kill", "--kill-whom=main", "--signal=SIGKILL", "gobfd")
			restarted := h.waitRestart(pid)
			h.expectShow("NRestarts", "1")
			assertSandbox(h, restarted)
			h.waitUp([]string{"single-hop", "vxlan", "geneve"}, 6)

			// A peer killed without its AdminDown lets the Detection Time expire,
			// which clears the unsolicited passive session's remote
			// discriminator, so its AdminDown cannot be sent (RFC 5880 6.8.7).
			h.mustRun("systemctl", "kill", "--signal=SIGKILL", "gobfd-peer")
			h.waitSessions(func(out string) bool { return !sessionUp(out, "10.10.0.11") })
			h.mustRun("systemctl", "stop", "gobfd")
			h.expectShow("Result", "success")
			h.expectShow("ExecMainStatus", "0")

			journal := h.mustRun("journalctl", "--unit=gobfd", "--output=cat", "--no-pager")
			if match := sandboxDenials.FindString(journal); match != "" {
				t.Fatalf("journal contains sandbox denial %q", match)
			}
			for line := range strings.Lines(journal) {
				if strings.Contains(line, "drain") {
					t.Logf("%s journal: %s", d.name, strings.TrimSpace(line))
				}
			}
			if !strings.Contains(journal, "AdminDown drain incomplete for an unreachable or unknown peer") ||
				strings.Contains(journal, "gobfd exited with error") {
				t.Error("dead-peer stop did not end with only the unknown-peer drain warning")
			}

			h.mustRun("systemctl", "enable", "gobfd")
			h.mustRun("systemctl", "start", "gobfd")
			h.expectShow("ActiveState", "active")
			h.expectShow("UnitFileState", "enabled")
			removalPID := h.mainPID()
			// The dummy bond fixture supports syntax verification, not an enforce-mode restart.
			h.copyFile(filepath.Join(root, "deployments/systemd/gobfd-kernel-bond.conf"),
				"/etc/systemd/system/gobfd.service.d/kernel-bond.conf")
			h.mustRun("systemd-analyze", "verify", "/usr/lib/systemd/system/gobfd.service")
			t.Logf("%s security: %s", d.name, exposure(h.mustRun("systemd-analyze", "security", "--no-pager", "gobfd")))

			h.mustRun(d.remove...)
			if code, _ := h.run("test", "-e", fmt.Sprintf("/proc/%d", removalPID)); code == 0 {
				t.Errorf("package removal left running daemon PID %d", removalPID)
			}
			if code, _ := h.run("test", "-L", "/etc/systemd/system/multi-user.target.wants/gobfd.service"); code == 0 {
				t.Error("package removal left the enablement symlink")
			}
			if code, _ := h.run("test", "-e", "/usr/lib/systemd/system/gobfd.service"); code == 0 {
				t.Error("package removal left gobfd.service")
			}
		})
		if containerID != "" {
			containertest.AssertContainerRemoved(t, endpoint, containerID)
		}
		if image != "" {
			containertest.AssertImageRemoved(t, endpoint, image)
		}
		if imageID != "" {
			containertest.AssertImageRemoved(t, endpoint, imageID)
		}
	}
}

type host struct {
	t   *testing.T
	ctx context.Context
	ctr testcontainers.Container
	// evidence records every command and its output for the E2E artifact.
	evidence *strings.Builder
}

// dumpOnFailure records the unit state before the container is removed.
func (h *host) dumpOnFailure() {
	if !h.t.Failed() {
		return
	}
	for _, argv := range [][]string{
		{"systemctl", "status", "--no-pager", "gobfd"},
		{"journalctl", "--unit=gobfd", "--no-pager", "--lines=80"},
	} {
		if _, out, err := containertest.Exec(context.WithoutCancel(h.ctx), h.ctr, argv); err == nil {
			h.t.Logf("%v:\n%s", argv, out)
			fmt.Fprintf(h.evidence, "$ %s\n%s\n", strings.Join(argv, " "), out)
		}
	}
}

func (h *host) run(argv ...string) (int, string) {
	h.t.Helper()
	code, out, err := containertest.Exec(h.ctx, h.ctr, argv)
	if err != nil {
		h.t.Fatalf("exec %v: %v", argv, err)
	}
	if h.evidence != nil {
		fmt.Fprintf(h.evidence, "$ %s (exit %d)\n%s\n", strings.Join(argv, " "), code, out)
	}
	return code, out
}

// writeEvidence stores the command log under the artifact directory, if any.
func (h *host) writeEvidence(runID, name string) {
	dir := os.Getenv(artifactEnv)
	if dir == "" {
		return
	}
	dir = filepath.Join(dir, runID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		h.t.Errorf("create evidence directory: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name+".log"), []byte(h.evidence.String()), 0o600); err != nil {
		h.t.Errorf("write %s evidence: %v", name, err)
	}
}

func (h *host) mustRun(argv ...string) string {
	h.t.Helper()
	code, out := h.run(argv...)
	if code != 0 {
		h.t.Fatalf("%v exited %d:\n%s", argv, code, out)
	}
	return out
}

func (h *host) upgradePackage(sourcePackage, enabledState string) {
	h.t.Helper()
	pid := h.mainPID()
	identity := h.mustRun("id", "gobfd")
	configuration := h.mustRun("sha256sum", "/etc/gobfd/gobfd.yml")
	current := strings.TrimSpace(h.mustRun("dpkg-query", "-W", "-f=${Version}", "gobfd"))
	version := current + "+qualification.1"
	packageRoot := strings.TrimSpace(h.mustRun("mktemp", "-d", "/tmp/gobfd-upgrade.XXXXXX"))
	h.mustRun("dpkg-deb", "--raw-extract", sourcePackage, packageRoot)
	controlPath := filepath.Join(packageRoot, "DEBIAN", "control")
	control := h.mustRun("cat", controlPath)
	original := strings.TrimSpace(h.mustRun("dpkg-deb", "--field", sourcePackage, "Version"))
	field := "Version: " + original + "\n"
	if original == "" || strings.Count(control, field) != 1 {
		h.t.Fatal("fixture control has no unique Version field")
	}
	// shortcut: version-only fixtures test dpkg phases; distinct binaries are needed for daemon upgrade qualification.
	h.copy(strings.Replace(control, field, "Version: "+version+"\n", 1), controlPath)
	for _, script := range []string{"postinst", "prerm"} {
		magic := strings.Fields(h.mustRun("od", "-An", "-tx1", "-N4", filepath.Join(packageRoot, "DEBIAN", script)))
		if strings.Join(magic, "") != "7f454c46" {
			h.t.Fatalf("Debian %s is not a native ELF maintainer program", script)
		}
	}
	archive := packageRoot + ".deb"
	h.mustRun("dpkg-deb", "-Zgzip", "--build", packageRoot, archive)
	if got := strings.TrimSpace(h.mustRun("dpkg-deb", "--field", archive, "Version")); got != version {
		h.t.Fatalf("upgrade fixture version = %q, want %q", got, version)
	}
	h.mustRun("dpkg", "--compare-versions", version, "gt", current)
	h.mustRun("dpkg", "--install", archive)
	if got := strings.TrimSpace(h.mustRun("dpkg-query", "-W", "-f=${Version}", "gobfd")); got != version {
		h.t.Fatalf("installed upgrade version = %q, want %q", got, version)
	}
	h.expectShow("ActiveState", "active")
	h.expectShow("UnitFileState", enabledState)
	if h.mainPID() != pid || h.mustRun("id", "gobfd") != identity ||
		h.mustRun("sha256sum", "/etc/gobfd/gobfd.yml") != configuration {
		h.t.Fatal("package upgrade changed the running PID, service identity or user configuration")
	}
}

func (h *host) copy(content, path string) {
	h.t.Helper()
	if err := h.ctr.CopyToContainer(h.ctx, []byte(content), path, 0o644); err != nil {
		h.t.Fatalf("copy %s: %v", path, err)
	}
}

func (h *host) copyFile(source, path string) {
	h.t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		h.t.Fatalf("read %s: %v", source, err)
	}
	h.mustRun("mkdir", "-p", filepath.Dir(path))
	h.copy(string(data), path)
}

func (h *host) show(property string) string {
	h.t.Helper()
	return strings.TrimSpace(h.mustRun("systemctl", "show", "--property="+property, "--value", "gobfd"))
}

func (h *host) expectShow(property, want string) {
	h.t.Helper()
	if got := h.show(property); got != want {
		h.t.Fatalf("gobfd.service %s = %q, want %q", property, got, want)
	}
}

func (h *host) mainPID() int {
	h.t.Helper()
	pid, err := strconv.Atoi(h.show("MainPID"))
	if err != nil || pid == 0 {
		h.t.Fatalf("MainPID = %q: %v", h.show("MainPID"), err)
	}
	return pid
}

func (h *host) waitRestart(old int) int {
	h.t.Helper()
	for {
		if h.show("ActiveState") == "active" {
			if pid, err := strconv.Atoi(h.show("MainPID")); err == nil && pid != 0 && pid != old {
				return pid
			}
		}
		h.pause("restart after SIGKILL")
	}
}

func (h *host) waitSessions(done func(string) bool) string {
	h.t.Helper()
	for {
		if code, out := h.run("gobfdctl", "session", "list"); code == 0 && done(out) {
			return out
		}
		h.pause("session condition")
	}
}

func (h *host) waitUp(types []string, minimum int) {
	h.t.Helper()
	h.waitSessions(func(out string) bool {
		up := 0
		seen := map[string]bool{}
		for line := range strings.Lines(out) {
			fields := strings.Fields(line)
			if len(fields) >= 5 && fields[4] == "Up" {
				up++
				seen[fields[3]] = true
			}
		}
		return up >= minimum && !slices.ContainsFunc(types, func(kind string) bool { return !seen[kind] })
	})
}

func (h *host) pause(what string) {
	h.t.Helper()
	select {
	case <-h.ctx.Done():
		h.t.Fatalf("wait for %s: %v", what, h.ctx.Err())
	case <-time.After(pollStep):
	}
}

func sessionUp(out, local string) bool {
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[2] == local && fields[4] == "Up" {
			return true
		}
	}
	return false
}

func assertSandbox(h *host, pid int) {
	h.t.Helper()
	status := h.mustRun("cat", fmt.Sprintf("/proc/%d/status", pid))
	for _, want := range []string{"Seccomp:\t2", "NoNewPrivs:\t1", "Umask:\t0077", "CapEff:\t0000000000003000"} {
		if !strings.Contains(status, want) {
			h.t.Errorf("MainPID status lacks %q", want)
		}
	}
	limits := h.mustRun("cat", fmt.Sprintf("/proc/%d/limits", pid))
	if !regexp.MustCompile(`Max open files\s+16384\s+16384`).MatchString(limits) {
		h.t.Errorf("MainPID descriptor limit is not 16384:\n%s", limits)
	}
	if !procMount(h.mustRun("cat", fmt.Sprintf("/proc/%d/mountinfo", pid))) {
		h.t.Error("service /proc lacks hidepid=invisible,subset=pid")
	}
	socket := strings.TrimSpace(h.mustRun("stat", "-c", "%a %U:%G", "/run/gobfd/gobfd.sock"))
	if got := socket; got != "660 gobfd:gobfd-admin" {
		h.t.Errorf("management socket = %q, want 660 gobfd:gobfd-admin", got)
	}
}

func procMount(mountinfo string) bool {
	scanner := bufio.NewScanner(strings.NewReader(mountinfo))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 4 && fields[4] == "/proc" &&
			strings.Contains(scanner.Text(), "hidepid=invisible") && strings.Contains(scanner.Text(), "subset=pid") {
			return true
		}
	}
	return false
}

func assertInstalledUnit(h *host, root string) {
	h.t.Helper()
	want, err := os.ReadFile(filepath.Join(root, "deployments/systemd/gobfd.service"))
	if err != nil {
		h.t.Fatalf("read repository unit: %v", err)
	}
	if got := h.mustRun("cat", "/usr/lib/systemd/system/gobfd.service"); got != string(want) {
		h.t.Fatal("installed gobfd.service differs from deployments/systemd/gobfd.service")
	}
}

func exposure(security string) string {
	for line := range strings.Lines(security) {
		if _, value, ok := strings.Cut(line, "Overall exposure level for gobfd.service:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return "unreported"
}

func sleepContext(ctx context.Context, t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("hold %s: %v", d, ctx.Err())
	case <-time.After(d):
	}
}

func findPackage(t *testing.T, dist, suffix string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dist, "gobfd_*_linux_"+runtime.GOARCH+suffix))
	if err != nil || len(matches) != 1 {
		t.Fatalf("want one gobfd %s package for %s in %s, got %v (%v)", suffix, runtime.GOARCH, dist, matches, err)
	}
	return matches[0]
}

// repositoryRoot relies on go test running the binary in the package directory.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve test directory: %v", err)
	}
	return filepath.Clean(filepath.Join(dir, "../.."))
}

// baseImage reuses the digest-pinned FROM of the release Containerfile.
func baseImage(t *testing.T, root, containerfile string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, containerfile))
	if err != nil {
		t.Fatalf("read %s: %v", containerfile, err)
	}
	for line := range strings.Lines(string(data)) {
		if image, ok := strings.CutPrefix(strings.TrimSpace(line), "FROM "); ok && strings.Contains(image, "@sha256:") {
			return strings.Fields(image)[0]
		}
	}
	t.Fatalf("%s has no digest-pinned FROM", containerfile)
	return ""
}

func buildSystemdImage(
	ctx context.Context, t *testing.T, endpoint, root string, d distro, runID string,
) (string, string) {
	t.Helper()
	buildContext := t.TempDir()
	base := baseImage(t, root, d.containerfile)
	containerfile := fmt.Sprintf("FROM %s\nRUN %s\n", base, d.installSystemd)
	if err := os.WriteFile(filepath.Join(buildContext, "Containerfile"), []byte(containerfile), 0o600); err != nil {
		t.Fatalf("write build Containerfile: %v", err)
	}
	podman, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client: %v", err)
	}
	revision, created, err := interopproject.BuildMetadata(ctx, root, os.Getenv("GOBFD_BUILD_REVISION"))
	if err != nil {
		t.Fatalf("resolve package fixture source identity: %v", err)
	}
	baseName, baseDigest, pinned := strings.Cut(base, "@")
	if !pinned || !containertest.IsImageDigest(baseDigest) {
		t.Fatalf("package fixture base is not digest-pinned: %s", base)
	}
	tag := fmt.Sprintf("pkg-%s-%d", d.name, time.Now().UnixNano())
	image := "localhost/gobfd-package-lifecycle:" + tag
	var imageID string
	if cleanupErr := containertest.RegisterImageCleanup(ctx, image, runID, packageTestKind, &imageID,
		podman, t.Cleanup, func(err error) { t.Errorf("%v", err) }); cleanupErr != nil {
		t.Fatalf("register package fixture image cleanup: %v", cleanupErr)
	}
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		t.Fatalf("create Podman provider: %v", err)
	}
	dockerProvider, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		t.Fatalf("Podman provider type = %T, want *testcontainers.DockerProvider: %v", provider, provider.Close())
	}
	labels := map[string]string{
		"io.gobfd.test": packageTestKind, "io.gobfd.run": runID, "io.gobfd.image": image,
		"org.opencontainers.image.source":   "https://github.com/dantte-lp/gobfd",
		"org.opencontainers.image.revision": revision, "org.opencontainers.image.created": created,
		"org.opencontainers.image.version":     "package-lifecycle-qualification",
		"org.opencontainers.image.licenses":    "Apache-2.0",
		"org.opencontainers.image.title":       "GoBFD package lifecycle fixture",
		"org.opencontainers.image.description": "Debian systemd install, upgrade and removal qualification",
		"org.opencontainers.image.base.name":   baseName, "org.opencontainers.image.base.digest": baseDigest,
	}
	_, buildErr := dockerProvider.BuildImage(ctx, systemdImageRequest(buildContext, tag, labels))
	metadata, inspectErr := podman.ImageMetadata(ctx, image)
	if inspectErr == nil {
		inspectErr = containertest.ValidateImageOwner(metadata, image, runID, packageTestKind)
		if inspectErr == nil {
			// Bind cleanup before validating public annotations or closing the provider.
			imageID = metadata.ID
			_, inspectErr = containertest.ValidateImageProvenance(metadata, revision)
		}
	}
	if buildCloseErr := errors.Join(buildErr, inspectErr, provider.Close()); buildCloseErr != nil {
		t.Fatalf("build %s systemd image: %v", d.name, buildCloseErr)
	}
	t.Logf("package fixture image=%s id=%s revision=%s created=%s", image, imageID, revision, created)
	return image, imageID
}

func systemdImageRequest(buildContext, tag string, labels map[string]string) *testcontainers.ContainerRequest {
	return &testcontainers.ContainerRequest{
		Context: buildContext, Dockerfile: "Containerfile",
		Repo: "localhost/gobfd-package-lifecycle", Tag: tag, KeepImage: true,
		BuildOptionsModifier: func(options *client.ImageBuildOptions) {
			// The package mirror must resolve through the host resolver.
			options.NetworkMode = "host"
			options.CPUPeriod, options.CPUQuota = 100000, 200000
			options.Memory, options.MemorySwap = 2<<30, 2<<30
			options.Labels = labels
		},
	}
}

func systemdContainerRequest(image, packages string) testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		Image:  image,
		Cmd:    []string{"/usr/lib/systemd/systemd"},
		Labels: map[string]string{"io.gobfd.test": packageTestKind},
		HostConfigModifier: func(hc *container.HostConfig) {
			// systemd builds the unit's mount namespace, seccomp filters and a
			// fresh procfs; the test reads the service's /proc entries.
			hc.CapAdd = []string{"SYS_ADMIN", "NET_ADMIN", "NET_RAW", "SYS_PTRACE"}
			hc.SecurityOpt = []string{"unmask=ALL", "label=disable"}
			hc.Binds = []string{packages + ":/packages:ro"}
			hc.CPUPeriod, hc.CPUQuota = 100000, 100000
			hc.CgroupnsMode = container.CgroupnsModePrivate
			hc.Memory, hc.MemorySwap = containerMemory, containerMemory
			pids := int64(containerPIDs)
			hc.PidsLimit = &pids
		},
	}
}

func startSystemd(ctx context.Context, t *testing.T, image, packages string) testcontainers.Container {
	t.Helper()
	ctr, err := containertest.Run(ctx, t, systemdContainerRequest(image, packages))
	if err != nil {
		t.Fatalf("start systemd container: %v", err)
	}
	h := &host{t: t, ctx: ctx, ctr: ctr}
	for _, limit := range []struct{ file, value string }{
		{"cpu.max", "100000 100000"},
		{"memory.max", strconv.Itoa(containerMemory)},
		{"memory.swap.max", "0"},
		{"pids.max", strconv.Itoa(containerPIDs)},
	} {
		if value := strings.TrimSpace(h.mustRun("cat", "/sys/fs/cgroup/"+limit.file)); value != limit.value {
			t.Fatalf("effective runtime %s = %q, want %q", limit.file, value, limit.value)
		}
	}
	// PID 1 opens its bus shortly after the container starts.
	for {
		code, out := h.run("systemctl", "is-system-running", "--wait")
		state := strings.TrimSpace(out)
		if state == "running" || state == "degraded" {
			return ctr
		}
		if !strings.Contains(state, "Failed to connect") {
			t.Fatalf("systemd state = %q (exit %d)", state, code)
		}
		h.pause("systemd boot")
	}
}

// peerNetns is the peer daemon's network namespace. Its commands use nsenter:
// ip -n and ip netns exec remount /sys in a new mount namespace, which
// rootless Podman refuses ("mount of /sys failed: Operation not permitted").
const peerNetns = "/run/netns/peer"

func topology() [][]string {
	in := func(args ...string) []string {
		return append([]string{"nsenter", "--net=" + peerNetns, "ip"}, args...)
	}
	steps := [][]string{
		{"ip", "netns", "add", "peer"},
		{"ip", "link", "add", "v0a", "type", "veth", "peer", "name", "v0b", "netns", "peer"},
		{"ip", "link", "add", "m1a", "type", "veth", "peer", "name", "m1b", "netns", "peer"},
		{"ip", "link", "add", "m2a", "type", "veth", "peer", "name", "m2b", "netns", "peer"},
		{"ip", "link", "add", "bond0", "type", "dummy"},
		in("link", "add", "bond0", "type", "dummy"),
	}
	for _, link := range []string{"v0a", "m1a", "m2a", "bond0", "lo"} {
		steps = append(steps, []string{"ip", "link", "set", link, "up"})
	}
	for _, link := range []string{"v0b", "m1b", "m2b", "bond0", "lo"} {
		steps = append(steps, in("link", "set", link, "up"))
	}
	return append(steps,
		[]string{"ip", "addr", "add", "10.10.0.1/24", "dev", "v0a"},
		[]string{"ip", "addr", "add", "10.10.0.11/24", "dev", "v0a"},
		[]string{"ip", "addr", "add", "fd00:10::1/64", "dev", "v0a", "nodad"},
		in("addr", "add", "10.10.0.2/24", "dev", "v0b"),
		in("addr", "add", "fd00:10::2/64", "dev", "v0b", "nodad"),
		[]string{"ip", "addr", "add", "10.30.0.1/32", "dev", "lo"},
		in("addr", "add", "10.30.0.2/32", "dev", "lo"),
		[]string{"ip", "route", "add", "10.30.0.2/32", "via", "10.10.0.2"},
		in("route", "add", "10.30.0.1/32", "via", "10.10.0.1"),
		[]string{"ip", "addr", "add", "10.20.0.1/32", "dev", "bond0"},
		in("addr", "add", "10.20.0.2/32", "dev", "bond0"),
		[]string{"ip", "route", "add", "10.20.0.2/32", "dev", "m1a", "metric", "10"},
		[]string{"ip", "route", "add", "10.20.0.2/32", "dev", "m2a", "metric", "20"},
		in("route", "add", "10.20.0.1/32", "dev", "m1b", "metric", "10"),
		in("route", "add", "10.20.0.1/32", "dev", "m2b", "metric", "20"),
	)
}

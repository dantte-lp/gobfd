package interop_test

import (
	"os"
	"os/exec" //nolint:depguard // Execution contracts run fixed fake tools with explicit arguments.
	"path/filepath"
	"strings"
	"testing"
)

const fakeTsharkImageID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRoutingCollectPcapPersistsExactOwnedTsharkImageID(t *testing.T) {
	routing := readRoutingRunner(t)
	body := shellFunctionBody(t, routing, "collect_pcap")

	tests := []struct {
		name        string
		environment []string
		wantSuccess bool
		wantErr     string
	}{
		{name: "missing owned container", environment: []string{"FAKE_CONTAINER_MISSING=1"}, wantErr: "absent or foreign"},
		{name: "inspect failure", environment: []string{"FAKE_INSPECT_FAIL=1"}, wantErr: "inspect immutable tshark image"},
		{
			name:        "invalid image ID",
			environment: []string{"FAKE_IMAGE_ID=sha256:invalid"},
			wantErr:     "invalid immutable tshark image ID",
		},
		{
			name: "image absent",
			environment: []string{
				"FAKE_IMAGE_ID=" + fakeTsharkImageID,
				"FAKE_IMAGE_EXISTS_FAIL=1",
			},
			wantErr: "immutable tshark image ID is unavailable",
		},
		{name: "success", environment: []string{"FAKE_IMAGE_ID=" + fakeTsharkImageID}, wantSuccess: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			report := filepath.Join(root, "report")
			if err := os.MkdirAll(filepath.Join(report, "interop"), 0o750); err != nil {
				t.Fatalf("create report fixture: %v", err)
			}
			fakePodman := filepath.Join(root, "podman")
			commandLog := filepath.Join(root, "podman.log")
			writeRoutingFakePodman(t, fakePodman)
			harness := filepath.Join(root, "collect-harness")
			writeRoutingHarness(t, harness, `
PODMAN=("${FAKE_PODMAN:?}")
REPORT_DIR="${TEST_REPORT_DIR:?}"
REPORT_REL=reports/e2e/routing/fake-run
DEV_PROJECT=v062-testcontainers
resolve_project_container_id() {
    if [[ "${FAKE_CONTAINER_MISSING:-0}" == 1 ]]; then return 1; fi
    printf '%s\n' immutable-tshark-id
}
interop_resolve_project_service_container_id() { printf '%s\n' immutable-dev-id; }
append_csv() { :; }
collect_pcap() {
`+body+`
}
collect_pcap interop gobfd-interop tshark-interop
`)

			command := exec.CommandContext(t.Context(), harness)
			command.Env = append(os.Environ(),
				"FAKE_PODMAN="+fakePodman,
				"FAKE_COMMAND_LOG="+commandLog,
				"TEST_REPORT_DIR="+report,
			)
			command.Env = append(command.Env, test.environment...)
			output, err := command.CombinedOutput()
			if test.wantSuccess && err != nil {
				t.Fatalf("collect pcap failed: %v\n%s", err, output)
			}
			if !test.wantSuccess {
				if err == nil {
					t.Fatalf("collect pcap unexpectedly passed\n%s", output)
				}
				packetErr, readErr := os.ReadFile(filepath.Join(report, "interop", "packets.err"))
				if readErr != nil {
					t.Fatalf("read collect diagnostic: %v", readErr)
				}
				if !strings.Contains(string(packetErr), test.wantErr) {
					t.Fatalf("collect diagnostic = %q, want %q", packetErr, test.wantErr)
				}
				if _, statErr := os.Stat(filepath.Join(report, "interop", "tshark-image-id")); !os.IsNotExist(statErr) {
					t.Fatalf("failed collection persisted image ID: %v", statErr)
				}
				return
			}

			imageID, err := os.ReadFile(filepath.Join(report, "interop", "tshark-image-id"))
			if err != nil {
				t.Fatalf("read persisted image ID: %v", err)
			}
			if string(imageID) != fakeTsharkImageID+"\n" {
				t.Fatalf("persisted image ID = %q, want exact ID", imageID)
			}
			logData, err := os.ReadFile(commandLog)
			if err != nil {
				t.Fatalf("read fake Podman log: %v", err)
			}
			logText := string(logData)
			for _, exact := range []string{
				"inspect --type container --format {{.Image}} immutable-tshark-id\n",
				"image exists " + fakeTsharkImageID + "\n",
				"exec immutable-dev-id go -C /app run ./test/e2e/routing/scripts/artifactmerge " +
					"write-image-id /app/reports/e2e/routing/fake-run/interop/tshark-image-id " +
					fakeTsharkImageID + "\n",
			} {
				if !strings.Contains(logText, exact) {
					t.Fatalf("Podman log missing %q:\n%s", exact, logText)
				}
			}
		})
	}
}

func TestRoutingMergeArtifactsUsesPersistedImageIDAndOwnedCleanup(t *testing.T) {
	routing := readRoutingRunner(t)
	body := shellFunctionBody(t, routing, "merge_artifacts")

	tests := []struct {
		name        string
		imageFile   string
		environment []string
		wantSuccess bool
	}{
		{name: "missing image ID"},
		{name: "invalid image ID", imageFile: "sha256:invalid\n"},
		{name: "image absent", imageFile: fakeTsharkImageID + "\n", environment: []string{"FAKE_IMAGE_EXISTS_FAIL=1"}},
		{name: "success", imageFile: fakeTsharkImageID + "\n", wantSuccess: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			report := filepath.Join(root, "report")
			for _, suite := range []string{"interop", "interop-bgp"} {
				suiteDir := filepath.Join(report, suite)
				if err := os.MkdirAll(suiteDir, 0o750); err != nil {
					t.Fatalf("create merge fixture directory: %v", err)
				}
				if err := os.WriteFile(filepath.Join(suiteDir, "containers.json"), []byte("[]\n"), 0o600); err != nil {
					t.Fatalf("write container fixture: %v", err)
				}
				if err := os.WriteFile(filepath.Join(suiteDir, "packets.pcapng"), []byte("pcap\n"), 0o600); err != nil {
					t.Fatalf("write pcap fixture: %v", err)
				}
			}
			if test.imageFile != "" {
				if err := os.WriteFile(
					filepath.Join(report, "interop", "tshark-image-id"), []byte(test.imageFile), 0o600,
				); err != nil {
					t.Fatalf("write image ID fixture: %v", err)
				}
			}

			fakePodman := filepath.Join(root, "podman")
			commandLog := filepath.Join(root, "podman.log")
			cleanupLog := filepath.Join(root, "cleanup.log")
			writeRoutingFakePodman(t, fakePodman)
			harness := filepath.Join(root, "merge-harness")
			writeRoutingHarness(t, harness, `
PODMAN=("${FAKE_PODMAN:?}")
REPORT_DIR="${TEST_REPORT_DIR:?}"
REPORT_REL=reports/e2e/routing/fake-run
DEV_PROJECT=v062-testcontainers
MERGE_OWNER_LABEL_KEY=io.gobfd.e2e.merge-owner
MERGE_OWNER_LABEL_VALUE=20260821T000000000000000Z-1
interop_query_labelled_container_ids() { :; }
interop_resolve_project_service_container_id() { printf '%s\n' immutable-dev-id; }
interop_remove_labelled_containers() { printf 'remove %s=%s\n' "$1" "$2" >>"${FAKE_CLEANUP_LOG:?}"; }
interop_verify_labelled_containers_absent() { printf 'verify %s=%s\n' "$1" "$2" >>"${FAKE_CLEANUP_LOG:?}"; }
merge_artifacts() {
`+body+`
}
merge_artifacts
`)

			command := exec.CommandContext(t.Context(), harness)
			command.Env = append(os.Environ(),
				"FAKE_PODMAN="+fakePodman,
				"FAKE_COMMAND_LOG="+commandLog,
				"FAKE_CLEANUP_LOG="+cleanupLog,
				"TEST_REPORT_DIR="+report,
			)
			command.Env = append(command.Env, test.environment...)
			output, err := command.CombinedOutput()
			if test.wantSuccess && err != nil {
				t.Fatalf("merge artifacts failed: %v\n%s", err, output)
			}
			if !test.wantSuccess && err == nil {
				t.Fatalf("merge artifacts unexpectedly passed\n%s", output)
			}
			logData, readErr := os.ReadFile(commandLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatalf("read merge Podman log: %v", readErr)
			}
			logText := string(logData)
			if !test.wantSuccess {
				for line := range strings.Lines(logText) {
					if strings.HasPrefix(line, "run ") {
						t.Fatalf("invalid image state reached Podman run:\n%s", logText)
					}
				}
				return
			}
			if strings.Contains(logText, "localhost/interop_tshark") {
				t.Fatalf("merge reused mutable tshark tag:\n%s", logText)
			}
			for _, exact := range []string{
				"image exists " + fakeTsharkImageID + "\n",
				"exec immutable-dev-id go -C /app run ./test/e2e/routing/scripts/artifactmerge " +
					"read-image-id /app/reports/e2e/routing/fake-run/interop/tshark-image-id\n",
				"exec immutable-dev-id go -C /app run ./test/e2e/routing/scripts/artifactmerge " +
					"merge /app/reports/e2e/routing/fake-run/containers.json " +
					"/app/reports/e2e/routing/fake-run/interop/containers.json " +
					"/app/reports/e2e/routing/fake-run/interop-bgp/containers.json\n",
				"run --label io.gobfd.e2e.merge-owner=20260821T000000000000000Z-1 --entrypoint /usr/bin/mergecap",
				fakeTsharkImageID + " -w /reports/packets.pcapng",
			} {
				if !strings.Contains(logText, exact) {
					t.Fatalf("merge Podman log missing %q:\n%s", exact, logText)
				}
			}
			cleanupData, readErr := os.ReadFile(cleanupLog)
			if readErr != nil {
				t.Fatalf("read cleanup log: %v", readErr)
			}
			wantCleanup := "remove io.gobfd.e2e.merge-owner=20260821T000000000000000Z-1\n" +
				"verify io.gobfd.e2e.merge-owner=20260821T000000000000000Z-1\n"
			if string(cleanupData) != wantCleanup {
				t.Fatalf("cleanup log = %q, want %q", cleanupData, wantCleanup)
			}
		})
	}
}

func readRoutingRunner(t *testing.T) string {
	t.Helper()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return readContractFile(t, "routing runner", filepath.Join(root, "test", "e2e", "routing", "run.sh"))
}

func writeRoutingHarness(t *testing.T, path, body string) {
	t.Helper()
	if err := writeExecutable(path, "#!/usr/bin/env bash\nset -euo pipefail\n"+body); err != nil {
		t.Fatalf("write routing harness: %v", err)
	}
}

func writeRoutingFakePodman(t *testing.T, path string) {
	t.Helper()
	if err := writeExecutable(path, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_COMMAND_LOG:?}"
case "${1:-} ${2:-}" in
  "inspect --type")
    if [[ "${FAKE_INSPECT_FAIL:-0}" == 1 ]]; then exit 42; fi
    printf '%s\n' "${FAKE_IMAGE_ID:-}"
    ;;
  "image exists")
    if [[ "${FAKE_IMAGE_EXISTS_FAIL:-0}" == 1 ]]; then exit 1; fi
    ;;
  "exec immutable-tshark-id")
    if [[ " $* " == *" cat /captures/bfd.pcapng "* ]]; then
      printf 'pcap\n'
    else
      printf 'frame.time_relative,ip.src\n0.1,172.20.0.10\n'
    fi
    ;;
  "exec immutable-dev-id")
    case " $* " in
      *" write-image-id "*)
        printf '%s\n' "${!#}" >"${TEST_REPORT_DIR:?}/interop/tshark-image-id"
        ;;
      *" read-image-id "*)
        cat "${TEST_REPORT_DIR:?}/interop/tshark-image-id"
        ;;
      *" merge "*)
        printf '{"suites":{"interop":[],"interop-bgp":[]}}\n' >"${TEST_REPORT_DIR:?}/containers.json"
        ;;
      *)
        printf 'unexpected artifact helper command: %s\n' "$*" >&2
        exit 98
        ;;
    esac
    ;;
  "run --label")
    ;;
  *)
    printf 'unexpected fake Podman command: %s\n' "$*" >&2
    exit 99
    ;;
esac
`); err != nil {
		t.Fatalf("write fake Podman: %v", err)
	}
}

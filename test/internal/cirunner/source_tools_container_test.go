//go:build linux

package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

type sourceToolsFixture struct {
	*syftCompilerFixture

	publisher        *goreleaserFixture
	creates          int
	tool, batchFault string
}

func newSourceToolsFixture(t *testing.T, fault string) *sourceToolsFixture {
	t.Helper()
	fixture := &sourceToolsFixture{
		syftCompilerFixture: newSyftCompilerFixture(t, ""),
		publisher:           newGoReleaserFixture(t), batchFault: fault,
	}
	fixture.root = fixture.publisher.root
	if err := os.MkdirAll(filepath.Join(fixture.root, "tools", "syft"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.path, []byte("/existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *sourceToolsFixture) options() ReleaseSyftOptions {
	options := fixture.syftCompilerFixture.options()
	options.Runner = fixture
	return options
}

func (fixture *sourceToolsFixture) buildInfo(reader io.ReaderAt) (*debug.BuildInfo, error) {
	file, ok := reader.(*os.File)
	if !ok {
		fixture.t.Fatal("source tool probe did not bind a file")
	}
	if strings.HasSuffix(file.Name(), goreleaserName) {
		return fixture.publisher.buildInfo(reader)
	}
	return &debug.BuildInfo{
		GoVersion: runtime.Version(), Path: toolprofile.SyftCommand,
		Main: debug.Module{Path: toolprofile.SyftModule, Version: syftFixtureVersion, Sum: toolprofile.SyftSourceSum},
	}, nil
}

func (fixture *sourceToolsFixture) RunCommand(ctx context.Context, spec CommandSpec) error {
	fixture.t.Helper()
	data, err := os.ReadFile(fixture.path)
	if err != nil || string(data) != "/existing\n" {
		fixture.t.Fatal("host PATH changed before both workers/probes/cleanups finished")
	}
	if spec.Name == goreleaserName {
		return fixture.publisher.RunCommand(ctx, spec)
	}
	if spec.Name == "docker" && spec.Args[0] == "create" {
		fixture.creates++
		fixture.tool = spec.Args[len(spec.Args)-1]
		fixture.removed = false
		if fixture.creates == 2 {
			fixture.id = strings.Repeat("b", 64)
			if fixture.batchFault == "foreign second worker" {
				fixture.fault = "foreign owner"
			}
		}
		return fixture.prepareWorker(ctx, spec)
	}
	if spec.Name == "docker" && spec.Args[0] == "start" && fixture.tool == "release-goreleaser" {
		switch fixture.batchFault {
		case "second build failure":
			return syscall.EIO
		case "unconfirmed second output":
			filename := filepath.Join(fixture.work, "install", "gobfd-goreleaser", "foreign")
			if mkdirErr := os.Mkdir(filepath.Dir(filename), 0o700); mkdirErr != nil {
				return mkdirErr
			}
			if writeErr := os.WriteFile(filename, []byte("foreign"), 0o600); writeErr != nil {
				return writeErr
			}
			return syscall.EIO
		case "cancel second worker":
			fixture.fault = "cancel worker"
			fixture.cancel()
			return ctx.Err()
		}
		return fixture.writePublisherWorker(ctx)
	}
	return fixture.syftCompilerFixture.RunCommand(ctx, spec)
}

func (fixture *sourceToolsFixture) prepareWorker(ctx context.Context, spec CommandSpec) error {
	if err := fixture.syftCompilerFixture.RunCommand(ctx, spec); err != nil {
		return err
	}
	temporary := filepath.Join(fixture.work, "cache", "tmp")
	info, statErr := os.Lstat(temporary)
	if statErr != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		fixture.t.Fatalf("worker temporary directory is not private: %v", statErr)
	}
	for _, key := range []string{"GOTMPDIR", "TMPDIR"} {
		if !slices.Contains(spec.Args, key+"="+temporary) {
			fixture.t.Fatalf("worker %s lacks its disk temporary path", fixture.tool)
		}
	}
	if err := os.WriteFile(filepath.Join(temporary, fixture.tool), []byte("worker temporary output"), 0o600); err != nil {
		return fmt.Errorf("write worker temporary output: %w", err)
	}
	return nil
}

func (fixture *sourceToolsFixture) writePublisherWorker(ctx context.Context) (returnErr error) {
	installation, err := prepareSourceInstallation(
		filepath.Join(fixture.work, "install", "gobfd-goreleaser"), goreleaserName, (*os.Root).Mkdir)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, installation.Close()) }()
	output := filepath.Join(installation.directory, "stage", goreleaserName)
	if writeErr := os.WriteFile(output, []byte("worker executable fixture"), 0o755); writeErr != nil {
		return writeErr
	}
	if openErr := installation.openExecutableAt(filepath.Join("stage", goreleaserName)); openErr != nil {
		return openErr
	}
	profile := fixture.publisher.profile
	receipt := goreleaserReceipt{
		Profile: profile, OriginalFiles: profile.Files, OriginalBytes: profile.Bytes,
		PatchedFiles: profile.Files, PatchedBytes: profile.Bytes + 1,
		PatchedContentSum:  goreleaserSHA256([]byte("worker source fixture")),
		ConsumerContentSum: profile.consumerContentSum(),
		Refmt: publisherComponentReceipt{
			OriginalFiles: profile.Refmt.Files, OriginalBytes: profile.Refmt.Bytes,
			PatchedFiles: profile.Refmt.Files, PatchedBytes: profile.Refmt.Bytes + 1,
			PatchedContentSum: goreleaserSHA256([]byte("worker Refmt fixture")),
		},
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := installation.publishPublisherProvenance(data); err != nil {
		return err
	}
	if err := installation.publish(); err != nil {
		return err
	}
	return installation.publishPath(ctx, filepath.Join(fixture.work, "github-path"))
}

func TestSourceToolBatchPublishesBothToolsAfterIndependentWorkers(t *testing.T) {
	t.Parallel()
	fixture := newSourceToolsFixture(t, "")
	if err := releaseSourceToolsCompiler(t.Context(), fixture.options(), fixture.executable,
		os.Getuid(), os.Getgid(), fixture.buildInfo); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteString("/existing\n")
	for _, tool := range []string{syftName, goreleaserName} {
		bin := filepath.Join(fixture.work, "install", "gobfd-"+tool, "bin")
		want.WriteString(bin + "\n")
		if _, err := os.Stat(filepath.Join(bin, tool)); err != nil {
			t.Fatalf("verified host tool is unavailable: %v", err)
		}
	}
	data, err := os.ReadFile(fixture.path)
	if err != nil || string(data) != want.String() || fixture.creates != 2 || fixture.exists {
		t.Fatalf("batch result PATH=%q, workers=%d, exists=%t, error=%v", data, fixture.creates, fixture.exists, err)
	}
	for _, path := range []string{
		fixture.executable, filepath.Join(fixture.work, "cache"),
		filepath.Join(fixture.work, "github-path"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transient batch path retained: %s: %v", path, err)
		}
	}
}

func TestSourceToolBatchFailurePreservesOnlyUncertainOwnedState(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{
		"second build failure", "cancel second worker", "foreign second worker",
		"unconfirmed second output",
		"wrong probe",
	} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fixture := newSourceToolsFixture(t, fault)
			if fault == "wrong probe" {
				fixture.publisher.fault = fault
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.cancel = cancel
			err := releaseSourceToolsCompiler(ctx, fixture.options(), fixture.executable,
				os.Getuid(), os.Getgid(), fixture.buildInfo)
			if err == nil {
				t.Fatal("failed second worker accepted")
			}
			if fault == "cancel second worker" && (!errors.Is(err, context.Canceled) || !fixture.cleanupAfterCancel) {
				t.Fatalf("cancellation or independent cleanup missing: %v", err)
			}
			data, readErr := os.ReadFile(fixture.path)
			if readErr != nil || string(data) != "/existing\n" {
				t.Fatalf("batch published partial PATH: %q %v", data, readErr)
			}
			for _, path := range []string{fixture.work, fixture.executable} {
				_, statErr := os.Lstat(path)
				if fault == "foreign second worker" || fault == "unconfirmed second output" {
					if statErr != nil {
						t.Fatalf("uncertain bind/controller removed: %s: %v", path, statErr)
					}
				} else if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("confirmed owned batch state retained: %s: %v", path, statErr)
				}
			}
			if fault == "unconfirmed second output" {
				filename := filepath.Join(fixture.work, "install", "gobfd-goreleaser", "foreign")
				data, readErr := os.ReadFile(filename)
				if readErr != nil || string(data) != "foreign" {
					t.Fatalf("unconfirmed output changed: %q %v", data, readErr)
				}
			}
		})
	}
}

func TestSourceToolWorkerSelectionIsClosed(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{syftName, goreleaserName, "other"} {
		args, err := sourceCompilerArguments("/checkout", "/private", "/runner/gobfd-cictl-20-2",
			1007, 1023, "owned", "token", tool)
		if tool == "other" {
			if err == nil || args != nil {
				t.Fatal("unknown worker accepted")
			}
			continue
		}
		if err != nil || !slices.Equal(args[len(args)-3:], []string{"600s", "/runner/gobfd-cictl-20-2", "release-" + tool}) {
			t.Fatalf("worker command escaped its individual timeout: %q %v", args, err)
		}
		for _, required := range []string{"GOTMPDIR=/private/cache/tmp", "TMPDIR=/private/cache/tmp"} {
			if !slices.Contains(args, required) {
				t.Fatalf("worker %s lacks owned disk temporary path %q", tool, required)
			}
		}
	}
}

func TestSourceToolBatchRequiresQualifiedPublisherBeforeAnyWorker(t *testing.T) {
	t.Parallel()
	for _, pair := range []string{"consumer", "source", "Refmt"} {
		t.Run(pair, func(t *testing.T) {
			t.Parallel()
			fixture := newSourceToolsFixture(t, "")
			switch pair {
			case "consumer":
				fixture.publisher.profile.Manifests = nil
			case "source":
				fixture.publisher.profile.SourceManifests = nil
			case "Refmt":
				fixture.publisher.profile.Refmt.Manifests = nil
			}
			fixture.publisher.writeProfile()
			if err := releaseSourceToolsCompiler(t.Context(), fixture.options(), fixture.executable,
				os.Getuid(), os.Getgid(), fixture.buildInfo); err == nil || fixture.creates != 0 {
				t.Fatal("unqualified publisher reached a compiler worker")
			}
		})
	}
}

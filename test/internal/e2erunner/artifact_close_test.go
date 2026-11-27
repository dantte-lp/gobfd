package e2erunner

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

var errInjectedArtifactClose = errors.New("injected artifact close failure")

func TestStrictArtifactWritersPropagateCloseError(t *testing.T) {
	t.Parallel()

	closeWithFailure := func(file *os.File) error {
		_ = file.Close()
		return errInjectedArtifactClose
	}

	t.Run("collectRoutingLogs", func(t *testing.T) {
		t.Parallel()

		reportDir := t.TempDir()
		logPath := filepath.Join(reportDir, containersLogName)
		if err := os.WriteFile(logPath, nil, 0o600); err != nil {
			t.Fatalf("seed routing container log: %v", err)
		}
		controller, err := interopproject.NewProject(reportDir, "gobfd-interop", "base", io.Discard, io.Discard)
		if err != nil {
			t.Fatalf("create project controller: %v", err)
		}
		r := &runner{
			root:      reportDir,
			reportDir: reportDir,
			stdout:    io.Discard,
			stderr:    io.Discard,
			closeFile: closeWithFailure,
		}
		err = r.collectRoutingLogs(t.Context(), controller, routingSuite{})
		if !errors.Is(err, errInjectedArtifactClose) {
			t.Fatalf("collectRoutingLogs() error = %v, want ErrInjectedArtifactClose", err)
		}
	})

	t.Run("commandToFile", func(t *testing.T) {
		t.Parallel()

		reportDir := t.TempDir()
		r := &runner{
			root:      reportDir,
			reportDir: reportDir,
			stdout:    io.Discard,
			stderr:    io.Discard,
			closeFile: closeWithFailure,
		}
		err := r.commandToFile(
			t.Context(),
			filepath.Join(reportDir, "out.log"),
			filepath.Join(reportDir, "err.log"),
			"true",
		)
		if !errors.Is(err, errInjectedArtifactClose) {
			t.Fatalf("commandToFile() error = %v, want ErrInjectedArtifactClose", err)
		}
	})

	t.Run("loggedCommandEnvironment", func(t *testing.T) {
		t.Parallel()

		reportDir := t.TempDir()
		r := &runner{
			root:      reportDir,
			reportDir: reportDir,
			stdout:    io.Discard,
			stderr:    io.Discard,
			closeFile: closeWithFailure,
		}
		err := r.loggedCommandEnvironment(t.Context(), time.Minute, nil, "printf", `{"Action":"pass"}\n`)
		if !errors.Is(err, errInjectedArtifactClose) {
			t.Fatalf("loggedCommandEnvironment() error = %v, want ErrInjectedArtifactClose", err)
		}
	})
}

func TestBestEffortFileReportsCloseErrorToStderr(t *testing.T) {
	t.Parallel()

	reportDir := t.TempDir()
	var stderr bytes.Buffer
	r := &runner{
		root:      reportDir,
		reportDir: reportDir,
		stdout:    io.Discard,
		stderr:    &stderr,
		closeFile: func(file *os.File) error {
			_ = file.Close()
			return errInjectedArtifactClose
		},
	}

	r.bestEffortFile(t.Context(), "best-effort.out", "best-effort.err", "true")

	logged := stderr.String()
	if !strings.Contains(logged, "best-effort artifact close failed") ||
		!strings.Contains(logged, errInjectedArtifactClose.Error()) {
		t.Fatalf("bestEffortFile() stderr = %q, want injected close error diagnostic", logged)
	}
}

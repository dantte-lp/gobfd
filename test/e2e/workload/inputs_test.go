//go:build linux && testcontainers

package workload_test

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type workloadInputs struct {
	binary           string
	binarySHA        string
	runID            string
	report           *os.Root
	controlDirectory string
}

const maxWorkloadBinarySize = 128 << 20

func loadWorkloadInputs(t *testing.T) workloadInputs {
	t.Helper()
	inputs := workloadInputs{
		binary: os.Getenv("GOBFD_WORKLOAD_BINARY"), runID: time.Now().UTC().Format("20060102T150405.000000000"),
	}
	if inputs.binary == "" {
		t.Skip("set GOBFD_WORKLOAD_BINARY to the pinned bfdload executable for the owned topology gate")
	}
	reportDirectory := os.Getenv("GOBFD_WORKLOAD_REPORT_DIR")
	if reportDirectory == "" {
		t.Fatal("GOBFD_WORKLOAD_REPORT_DIR is required to retain topology evidence")
	}
	info, statErr := os.Stat(inputs.binary)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxWorkloadBinarySize {
		t.Fatal("workload executable must be a nonempty regular file of at most 128 MiB")
	}
	build, err := buildinfo.ReadFile(inputs.binary)
	if err != nil {
		t.Fatal(err)
	}
	if build.GoVersion != "go1.27.3" || build.Path != "github.com/dantte-lp/gobfd/test/cmd/bfdload" {
		t.Fatalf("unexpected workload executable: Go=%s path=%s", build.GoVersion, build.Path)
	}
	file, err := os.Open(inputs.binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	hashedBytes, hashErr := io.Copy(hash, io.LimitReader(file, maxWorkloadBinarySize+1))
	closeErr := file.Close()
	if hashErr != nil || closeErr != nil || hashedBytes > maxWorkloadBinarySize {
		t.Fatalf("hash workload executable: read=%v close=%v", hashErr, closeErr)
	}
	inputs.binarySHA = hex.EncodeToString(hash.Sum(nil))
	if mkdirErr := os.MkdirAll(filepath.Join(reportDirectory, inputs.runID), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	inputs.report, err = os.OpenRoot(filepath.Join(reportDirectory, inputs.runID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputs.report.Close(); err != nil {
			t.Error(err)
		}
	})
	inputs.controlDirectory, err = filepath.Abs(filepath.Join(reportDirectory, inputs.runID, "control"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inputs.report.Mkdir("control", 0o700); err != nil {
		t.Fatal(err)
	}
	inputs.save(t, "binary-build.json", build)
	inputs.write(t, "binary-sha256.txt", []byte(inputs.binarySHA+"\n"))
	return inputs
}

func (i workloadInputs) save(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	i.write(t, name, data)
}

func (i workloadInputs) write(t *testing.T, name string, data []byte) {
	t.Helper()
	file, err := i.report.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write owned evidence %s: write=%v close=%v", name, writeErr, closeErr)
	}
}

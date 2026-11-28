//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWorkloadSignalHelper(_ *testing.T) {
	if len(os.Args) < 4 || os.Args[2] != "bfdload-workload-signal-helper" {
		return
	}
	os.Args = append([]string{os.Args[0]}, os.Args[3:]...)
	main()
}

func TestWorkloadSignalWritesPartialReport(t *testing.T) {
	for _, command := range []string{"peers", "owner"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(command+"/"+sig.String(), func(t *testing.T) {
				listener, listenErr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 31, 0, 5), Port: 37979})
				if listenErr != nil {
					t.Fatal(listenErr)
				}
				t.Cleanup(func() {
					if err := listener.Close(); err != nil {
						t.Error(err)
					}
				})
				reader, writer, pipeErr := os.Pipe()
				if pipeErr != nil {
					t.Fatal(pipeErr)
				}
				t.Cleanup(func() {
					if err := reader.Close(); err != nil {
						t.Error(err)
					}
				})
				executable, executableErr := os.Executable()
				if executableErr != nil {
					t.Fatal(executableErr)
				}
				endpoint := "127.31.0.5:37979,127.31.0.6:37980"
				if command == "owner" {
					endpoint = "127.0.0.1:37980,127.31.0.5:37979"
				}
				child, startErr := os.StartProcess(executable, []string{
					executable, "-test.run=^TestWorkloadSignalHelper$",
					"bfdload-workload-signal-helper", command, "fixture", "--sessions=1", "--duration=10s",
					"--source-port-base=55000", "--session=" + endpoint,
				},
					&os.ProcAttr{Files: []*os.File{os.Stdin, writer, os.Stderr}})
				closeErr := writer.Close()
				if startErr != nil {
					t.Fatal(startErr)
				}
				var state *os.ProcessState
				var waitErr error
				done := make(chan struct{})
				go func() {
					state, waitErr = child.Wait()
					close(done)
				}()
				t.Cleanup(func() {
					select {
					case <-done:
					default:
						if err := child.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
							t.Error(err)
						}
						<-done
					}
				})
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				if err := listener.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var packet [64]byte
				if _, _, err := listener.ReadFromUDPAddrPort(packet[:]); err != nil {
					t.Fatal(err)
				}
				if err := child.Signal(sig); err != nil {
					t.Fatal(err)
				}
				if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				output, outputErr := io.ReadAll(reader)
				if outputErr != nil {
					t.Fatal(outputErr)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("signal did not terminate workload after writing its report")
				}
				if waitErr != nil || state.ExitCode() != 1 {
					t.Fatalf("signal did not produce a bounded error exit: state=%v error=%v", state, waitErr)
				}
				var report struct {
					Completed          bool   `json:"completed"`
					ReferenceQualified bool   `json:"reference_qualified"`
					Error              string `json:"error"`
					Stopped            bool   `json:"stopped"`
					Final              struct {
						Stopped bool `json:"stopped"`
					} `json:"final"`
				}
				if err := json.Unmarshal(output, &report); err != nil {
					t.Fatalf("partial report missing: %v", err)
				}
				if report.Completed || report.ReferenceQualified || report.Error == "" ||
					(!report.Stopped && !report.Final.Stopped) {
					t.Fatalf("signal report lost cleanup/error evidence: %s", output)
				}
			})
		}
	}
}

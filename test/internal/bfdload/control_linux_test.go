//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestControlStartPreservesClockDomainAndPublication(t *testing.T) {
	t.Parallel()
	control, err := openRunControl(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := control.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if start, found, readErr := control.readStart(t.Context()); readErr != nil || found || start != nil {
		t.Fatalf("unpublished start treated as ready: %+v %t %v", start, found, readErr)
	}
	namespace, err := timeNamespaceID()
	if err != nil {
		t.Fatal(err)
	}
	now, err := monotonicNowNS()
	if err != nil {
		t.Fatal(err)
	}
	deadline := now + int64(time.Second)
	value := fmt.Sprintf("%d %d\n", namespace, deadline)
	if publishErr := control.publish("start", value); publishErr != nil {
		t.Fatal(publishErr)
	}
	start, found, err := control.readStart(t.Context())
	if err != nil || !found || start.NativeStartNS != deadline || start.TimeNamespaceID != namespace ||
		start.BracketNS < 0 || start.BracketNS > int64(time.Millisecond) {
		t.Fatalf("start lost native epoch or measured conversion bracket: %+v %t %v", start, found, err)
	}
	if until := time.Until(start.goStart); until <= 0 || until > time.Second {
		t.Fatalf("native monotonic deadline mapped to wrong Go clock: %s", until)
	}
	if publishErr := control.publish("start", "replacement"); !errors.Is(publishErr, os.ErrExist) {
		t.Fatalf("existing control input overwritten: %v", publishErr)
	}
	data, err := control.root.ReadFile("start")
	if err != nil || string(data) != value {
		t.Fatalf("atomic publication changed accepted data: %q: %v", data, err)
	}
}

func TestControlRejectsForeignEpochAndMalformedInput(t *testing.T) {
	t.Parallel()
	namespace, err := timeNamespaceID()
	if err != nil {
		t.Fatal(err)
	}
	now, err := monotonicNowNS()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"", "1", "1 2 3", "-1 2", "1 -2", "\xff\n", strings.Repeat("9", 65),
		fmt.Sprintf("%d %d", namespace+1, now+int64(time.Second)),
		fmt.Sprintf("%d %d", namespace, now-int64(time.Second)),
		fmt.Sprintf("%d %d", namespace, now+int64(2*maxStartDelay)),
	} {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "start"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		control, err := openRunControl(directory)
		if err != nil {
			t.Fatal(err)
		}
		_, _, readErr := control.readStart(t.Context())
		closeErr := control.Close()
		if !errors.Is(readErr, ErrControl) || closeErr != nil {
			t.Fatalf("unverifiable start accepted: %q read=%v close=%v", value, readErr, closeErr)
		}
	}
}

func TestControlRejectsSpecialFilesAndCanceledReads(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	control, err := openRunControl(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := control.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := unix.Mkfifo(filepath.Join(directory, "start"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.readStart(t.Context()); !errors.Is(err, ErrControl) {
		t.Fatalf("non-regular input accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := control.readStart(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, name := range []string{"", ".", "../outside", "nested/start"} {
		if err := control.publish(name, "1"); !errors.Is(err, ErrControl) {
			t.Fatalf("invalid publication name accepted %q: %v", name, err)
		}
	}
}

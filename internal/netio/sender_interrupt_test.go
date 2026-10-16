//go:build linux

package netio

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

func TestUDPSenderExposesOwnedWriteInterrupt(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sender := &UDPSender{conn: conn, logger: slog.New(slog.DiscardHandler)}
	interrupt, ok := any(sender).(interface{ InterruptWrite() error })
	if !ok {
		t.Fatal("UDP sender cannot interrupt a pending owned write")
	}
	if err := interrupt.InterruptWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteToUDP([]byte{1}, conn.LocalAddr().(*net.UDPAddr)); err == nil {
		t.Error("write after owned interruption succeeded")
	}
}

type failingWriteDeadline struct{ err error }

func (d failingWriteDeadline) SetWriteDeadline(time.Time) error { return d.err }

func TestOwnedWriteInterruption(t *testing.T) {
	t.Run("pending-write", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()
			result := make(chan error, 1)
			go func() { _, err := local.Write([]byte{1}); result <- err }()
			synctest.Wait()
			if len(result) != 0 {
				t.Fatal("pipe write did not block")
			}
			if err := interruptOwnedWrite(local, local.Close); err != nil {
				t.Error(err)
			}
			if err := <-result; !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Errorf("pending write error=%v", err)
			}
		})
	})
	t.Run("deadline-and-close-errors", func(t *testing.T) {
		deadlineErr := errors.New("set deadline failed")
		closeErr := errors.New("close failed")
		closed := 0
		err := interruptOwnedWrite(failingWriteDeadline{deadlineErr}, func() error { closed++; return closeErr })
		if !errors.Is(err, deadlineErr) || !errors.Is(err, closeErr) || closed != 1 {
			t.Errorf("interrupt error=%v close calls=%d", err, closed)
		}
	})
}

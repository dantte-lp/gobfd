//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ErrControl rejects unverifiable workload coordination input.
var ErrControl = errors.New("invalid workload control")

const (
	maxControlBytes = 64
	startFieldCount = 2
	maxStartDelay   = time.Minute
	maxClockBracket = time.Millisecond
)

// Control owns a shared directory for the isolated workload's phase protocol.
type Control struct{ root *os.Root }

type phaseStart struct {
	NativeStartNS   int64
	TimeNamespaceID uint64
	BracketNS       int64
	goStart         time.Time
}

// Phase reports a common monotonic epoch; it is not an RX uncertainty bound.
type Phase struct {
	NativeStartNS   int64  `json:"native_start_ns"`
	TimeNamespaceID uint64 `json:"time_namespace_id"`
	BracketNS       int64  `json:"bracket_ns"`
}

func openRunControl(directory string) (*Control, error) {
	info, statErr := os.Lstat(directory)
	if statErr != nil {
		return nil, fmt.Errorf("inspect control directory: %w", statErr)
	}
	var native unix.Stat_t
	if err := unix.Lstat(directory, &native); err != nil {
		return nil, fmt.Errorf("inspect control owner: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || int(native.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("control directory must be private and owned by the process: %w", ErrControl)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open workload control directory: %w", err)
	}
	opened, openedErr := root.Stat(".")
	if openedErr != nil || !os.SameFile(info, opened) {
		return nil, errors.Join(ErrControl, openedErr, root.Close())
	}
	return &Control{root: root}, nil
}

// OpenControl opens an already-created private workload control directory.
func OpenControl(directory string) (*Control, error) { return openRunControl(directory) }

// Close releases the directory descriptor without deleting phase evidence.
func (c *Control) Close() error {
	if err := c.root.Close(); err != nil {
		return fmt.Errorf("close workload control directory: %w", err)
	}
	return nil
}

// publish exposes complete bytes once; linking refuses an existing destination.
func (c *Control) publish(name, value string) (resultErr error) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") ||
		len(name) > maxControlBytes || len(value) > maxControlBytes {
		return fmt.Errorf("control name or value outside bound: %w", ErrControl)
	}
	temporary := fmt.Sprintf(".%s.%d", name, os.Getpid())
	file, err := c.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create control publication %s: %w", name, err)
	}
	defer func() {
		if removeErr := c.root.Remove(temporary); removeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove control staging file: %w", removeErr))
		}
	}()
	_, writeErr := io.WriteString(file, value)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write control publication %s: %w", name, err)
	}
	if err := c.root.Link(temporary, name); err != nil {
		return fmt.Errorf("publish control file %s: %w", name, err)
	}
	return nil
}

func (c *Control) read(ctx context.Context, name string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("read workload control: %w", err)
	}
	file, err := c.root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open control input %s: %w: %w", name, ErrControl, err)
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() {
		return nil, false, errors.Join(ErrControl, statErr, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxControlBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, false, fmt.Errorf("read control input %s: %w: %w", name, ErrControl, err)
	}
	if len(data) > maxControlBytes {
		return nil, false, fmt.Errorf("control input %s exceeds byte bound: %w", name, ErrControl)
	}
	return data, true, nil
}

func (c *Control) readStart(ctx context.Context) (*phaseStart, bool, error) {
	data, found, err := c.read(ctx, "start")
	if err != nil || !found {
		return nil, found, err
	}
	fields := strings.Fields(string(data))
	if len(fields) != startFieldCount {
		return nil, false, fmt.Errorf("start requires namespace and monotonic deadline: %w", ErrControl)
	}
	namespace, namespaceErr := strconv.ParseUint(fields[0], 10, 64)
	deadline, deadlineErr := strconv.ParseInt(fields[1], 10, 64)
	if parseErr := errors.Join(namespaceErr, deadlineErr); parseErr != nil {
		return nil, false, fmt.Errorf("parse control start: %w: %w", ErrControl, parseErr)
	}
	actualNamespace, err := timeNamespaceID()
	if err != nil {
		return nil, false, err
	}
	before := time.Now()
	nativeNow, err := monotonicNowNS()
	after := time.Now()
	if err != nil {
		return nil, false, err
	}
	remaining, bracket := time.Duration(deadline-nativeNow), after.Sub(before)
	if namespace == 0 || namespace != actualNamespace || deadline <= nativeNow ||
		remaining > maxStartDelay || bracket < 0 || bracket > maxClockBracket {
		return nil, false, fmt.Errorf("start clock domain, deadline or conversion bracket rejected: %w", ErrControl)
	}
	return &phaseStart{
		NativeStartNS: deadline, TimeNamespaceID: namespace, BracketNS: int64(bracket),
		goStart: after.Add(remaining),
	}, true, nil
}

func timeNamespaceID() (uint64, error) {
	var stat unix.Stat_t
	if err := unix.Stat("/proc/self/ns/time", &stat); err != nil {
		return 0, fmt.Errorf("stat workload time namespace: %w", err)
	}
	return stat.Ino, nil
}

func monotonicNowNS() (int64, error) {
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		return 0, fmt.Errorf("read workload monotonic clock: %w", err)
	}
	return now.Nano(), nil
}

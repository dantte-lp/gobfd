package netio_test

import (
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// MockPacketConn — Test double for PacketConn
// -------------------------------------------------------------------------

// MockPacketConn implements netio.PacketConn for testing without real sockets.
// It provides injectable read/write behavior and records method calls.
type MockPacketConn struct {
	mu        sync.Mutex
	localAddr netip.AddrPort
	closed    bool

	// ReadFunc is called by ReadPacket. Set this to control read behavior.
	ReadFunc func(buf []byte) (int, netio.PacketMeta, error)

	// WriteFunc is called by WritePacket. Set this to control write behavior.
	WriteFunc func(buf []byte, dst netip.Addr) error

	// Written records all packets sent via WritePacket.
	Written []writtenPacket
}

// writtenPacket records a single WritePacket call.
type writtenPacket struct {
	Data []byte
	Dst  netip.Addr
}

// NewMockPacketConn creates a MockPacketConn with the given local address.
func NewMockPacketConn(addr netip.AddrPort) *MockPacketConn {
	return &MockPacketConn{
		localAddr: addr,
	}
}

// ReadPacket implements PacketConn.ReadPacket using the injectable ReadFunc.
func (m *MockPacketConn) ReadPacket(buf []byte) (int, netio.PacketMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, netio.PacketMeta{}, netio.ErrSocketClosed
	}
	if m.ReadFunc != nil {
		return m.ReadFunc(buf)
	}
	return 0, netio.PacketMeta{}, errors.New("mock: ReadFunc not set")
}

// WritePacket implements PacketConn.WritePacket.
func (m *MockPacketConn) WritePacket(buf []byte, dst netip.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return netio.ErrSocketClosed
	}

	// Copy the buffer so the test can inspect it after the caller reuses it.
	data := make([]byte, len(buf))
	copy(data, buf)
	m.Written = append(m.Written, writtenPacket{Data: data, Dst: dst})

	if m.WriteFunc != nil {
		return m.WriteFunc(buf, dst)
	}
	return nil
}

// Close implements PacketConn.Close.
func (m *MockPacketConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	return nil
}

// LocalAddr implements PacketConn.LocalAddr.
func (m *MockPacketConn) LocalAddr() netip.AddrPort {
	return m.localAddr
}

// -------------------------------------------------------------------------
// Tests — Source Port Allocator
// -------------------------------------------------------------------------

// TestSourcePortAllocatorBasic verifies that a single allocation returns a
// port in the RFC 5881 Section 4 range (49152-65535) and that it can be
// released successfully.
func TestSourcePortAllocatorBasic(t *testing.T) {
	t.Parallel()
	requireLinuxSourcePortAllocator(t)

	alloc := netio.NewSourcePortAllocator()

	port, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("allocate: unexpected error: %v", err)
	}

	if port < 49152 {
		t.Errorf("port %d below RFC 5881 minimum 49152", port)
	}

	// Release should not panic.
	alloc.Release(port)

	// Double release should be a no-op.
	alloc.Release(port)
}

// TestSourcePortAllocatorUnique verifies that multiple consecutive
// allocations return unique ports.
func TestSourcePortAllocatorUnique(t *testing.T) {
	t.Parallel()
	requireLinuxSourcePortAllocator(t)

	alloc := netio.NewSourcePortAllocator()
	seen := make(map[uint16]struct{}, 100)

	for i := range 100 {
		port, err := alloc.Allocate()
		if err != nil {
			t.Fatalf("allocation %d: unexpected error: %v", i, err)
		}
		if _, exists := seen[port]; exists {
			t.Fatalf("allocation %d: duplicate port %d", i, port)
		}
		seen[port] = struct{}{}
	}

	if len(seen) != 100 {
		t.Errorf("expected 100 unique ports, got %d", len(seen))
	}
}

// TestSourcePortAllocatorRangeValidation verifies all allocated ports are
// within the RFC 5881 Section 4 mandated range.
func TestSourcePortAllocatorRangeValidation(t *testing.T) {
	t.Parallel()
	requireLinuxSourcePortAllocator(t)

	alloc := netio.NewSourcePortAllocator()

	for i := range 200 {
		port, err := alloc.Allocate()
		if err != nil {
			t.Fatalf("allocation %d: unexpected error: %v", i, err)
		}
		if port < 49152 {
			t.Errorf("allocation %d: port %d below minimum 49152", i, port)
		}
	}
}

// TestSourcePortAllocatorReleaseAndReuse verifies that released ports can
// be reallocated.
func TestSourcePortAllocatorReleaseAndReuse(t *testing.T) {
	t.Parallel()
	requireLinuxSourcePortAllocator(t)

	alloc := netio.NewSourcePortAllocator()

	// Allocate a port and release it.
	port1, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("first allocate: %v", err)
	}

	alloc.Release(port1)

	// Allocate many ports; eventually the released port should be reused
	// (we cannot guarantee the exact port but we verify no errors).
	for i := range 50 {
		p, allocErr := alloc.Allocate()
		if allocErr != nil {
			t.Fatalf("allocation %d after release: %v", i, allocErr)
		}
		alloc.Release(p)
	}
}

// TestSourcePortAllocatorConcurrency verifies thread-safety of the
// allocator under concurrent access. Run with -race to detect races.
func TestSourcePortAllocatorConcurrency(t *testing.T) {
	t.Parallel()
	requireLinuxSourcePortAllocator(t)

	alloc := netio.NewSourcePortAllocator()

	const (
		numGoroutines = 10
		numPerRoutine = 50
	)

	results := make([][]uint16, numGoroutines)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		results[g] = make([]uint16, 0, numPerRoutine)
		go func(idx int) {
			defer wg.Done()
			for range numPerRoutine {
				port, err := alloc.Allocate()
				if err != nil {
					t.Errorf("goroutine %d: allocate: %v", idx, err)
					return
				}
				results[idx] = append(results[idx], port)
			}
		}(g)
	}

	wg.Wait()

	// Verify all ports are unique across goroutines.
	seen := make(map[uint16]struct{}, numGoroutines*numPerRoutine)
	for g, ports := range results {
		for i, port := range ports {
			if _, exists := seen[port]; exists {
				t.Errorf("goroutine %d, allocation %d: duplicate port %d", g, i, port)
			}
			seen[port] = struct{}{}
		}
	}

	total := numGoroutines * numPerRoutine
	if len(seen) != total {
		t.Errorf("expected %d unique ports, got %d", total, len(seen))
	}

	// Release all ports.
	for _, ports := range results {
		for _, port := range ports {
			alloc.Release(port)
		}
	}
}

func requireLinuxSourcePortAllocator(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("source port allocation requires the Linux transport")
	}
}

// -------------------------------------------------------------------------
// Tests — GTSM TTL Validation
// -------------------------------------------------------------------------

type ttlValidationCase struct {
	name    string
	value   uint8
	wantErr bool
}

type packetMetaForTTL func(uint8) netio.PacketMeta

func ipv4PacketMeta(ttl uint8) netio.PacketMeta {
	return netio.PacketMeta{TTL: ttl}
}

func ipv6PacketMeta(hopLimit uint8) netio.PacketMeta {
	return netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("2001:db8::1"),
		DstAddr: netip.MustParseAddr("2001:db8::2"),
		TTL:     hopLimit,
	}
}

func testValidateTTL(
	t *testing.T,
	valueName string,
	multiHop bool,
	metaForValue packetMetaForTTL,
	tests []ttlValidationCase,
) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meta := metaForValue(tt.value)
			err := netio.ValidateTTL(meta, multiHop)

			if tt.wantErr && err == nil {
				t.Errorf("%s %d: expected error, got nil", valueName, tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("%s %d: unexpected error: %v", valueName, tt.value, err)
			}
			if tt.wantErr && err != nil && !errors.Is(err, netio.ErrTTLInvalid) {
				t.Errorf("%s %d: error does not wrap ErrTTLInvalid: %v", valueName, tt.value, err)
			}
		})
	}
}

// TestValidateTTLSingleHop verifies that single-hop sessions require
// TTL = 255 exactly (RFC 5881 Section 5, RFC 5082).
func TestValidateTTLSingleHop(t *testing.T) {
	t.Parallel()

	testValidateTTL(t, "TTL", false, ipv4PacketMeta, []ttlValidationCase{
		{name: "TTL 255 valid", value: 255, wantErr: false},
		{name: "TTL 254 invalid", value: 254, wantErr: true},
		{name: "TTL 0 invalid", value: 0, wantErr: true},
		{name: "TTL 128 invalid", value: 128, wantErr: true},
		{name: "TTL 1 invalid", value: 1, wantErr: true},
	})
}

// TestValidateTTLMultiHop verifies that multi-hop sessions require
// TTL >= 254 (RFC 5883 Section 2).
func TestValidateTTLMultiHop(t *testing.T) {
	t.Parallel()

	testValidateTTL(t, "TTL", true, ipv4PacketMeta, []ttlValidationCase{
		{name: "TTL 255 valid", value: 255, wantErr: false},
		{name: "TTL 254 valid", value: 254, wantErr: false},
		{name: "TTL 253 invalid", value: 253, wantErr: true},
		{name: "TTL 0 invalid", value: 0, wantErr: true},
		{name: "TTL 128 invalid", value: 128, wantErr: true},
	})
}

// -------------------------------------------------------------------------
// Tests — IPv6 GTSM Hop Limit Validation
// -------------------------------------------------------------------------

// TestValidateHopLimitSingleHopIPv6 verifies that single-hop IPv6 sessions
// require HopLimit = 255 exactly, same as IPv4 TTL (RFC 5881 Section 5,
// RFC 5082). The PacketMeta.TTL field stores the IPv6 Hop Limit.
func TestValidateHopLimitSingleHopIPv6(t *testing.T) {
	t.Parallel()

	testValidateTTL(t, "HopLimit", false, ipv6PacketMeta, []ttlValidationCase{
		{name: "HopLimit 255 valid", value: 255, wantErr: false},
		{name: "HopLimit 254 invalid", value: 254, wantErr: true},
		{name: "HopLimit 0 invalid", value: 0, wantErr: true},
		{name: "HopLimit 128 invalid", value: 128, wantErr: true},
		{name: "HopLimit 64 invalid", value: 64, wantErr: true},
	})
}

// TestValidateHopLimitMultiHopIPv6 verifies that multi-hop IPv6 sessions
// require HopLimit >= 254 (RFC 5883 Section 2). Same rules as IPv4.
func TestValidateHopLimitMultiHopIPv6(t *testing.T) {
	t.Parallel()

	testValidateTTL(t, "HopLimit", true, ipv6PacketMeta, []ttlValidationCase{
		{name: "HopLimit 255 valid", value: 255, wantErr: false},
		{name: "HopLimit 254 valid", value: 254, wantErr: false},
		{name: "HopLimit 253 invalid", value: 253, wantErr: true},
		{name: "HopLimit 0 invalid", value: 0, wantErr: true},
		{name: "HopLimit 128 invalid", value: 128, wantErr: true},
	})
}

// -------------------------------------------------------------------------
// Tests — MockPacketConn
// -------------------------------------------------------------------------

// TestMockPacketConnWrite verifies that WritePacket records the packet data
// and destination address correctly.
func TestMockPacketConnWrite(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	dst := netip.MustParseAddr("10.0.0.1")
	payload := []byte{0x20, 0x40, 0x03, 0x18, 0x00, 0x00, 0x00, 0x01}

	err := mock.WritePacket(payload, dst)
	if err != nil {
		t.Fatalf("write: unexpected error: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.Written) != 1 {
		t.Fatalf("expected 1 written packet, got %d", len(mock.Written))
	}

	if mock.Written[0].Dst != dst {
		t.Errorf("dst = %s, want %s", mock.Written[0].Dst, dst)
	}

	if len(mock.Written[0].Data) != len(payload) {
		t.Errorf("data length = %d, want %d", len(mock.Written[0].Data), len(payload))
	}
}

// TestMockPacketConnRead verifies that ReadPacket calls the injected
// ReadFunc and returns its results.
func TestMockPacketConnRead(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	wantMeta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.2"),
		TTL:     255,
		IfIndex: 3,
		IfName:  "eth0",
	}
	wantData := []byte{0x20, 0x40, 0x03, 0x18}

	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		n := copy(buf, wantData)
		return n, wantMeta, nil
	}

	buf := make([]byte, 64)
	n, meta, err := mock.ReadPacket(buf)
	if err != nil {
		t.Fatalf("read: unexpected error: %v", err)
	}

	if n != len(wantData) {
		t.Errorf("n = %d, want %d", n, len(wantData))
	}
	if meta.SrcAddr != wantMeta.SrcAddr {
		t.Errorf("src = %s, want %s", meta.SrcAddr, wantMeta.SrcAddr)
	}
	if meta.TTL != wantMeta.TTL {
		t.Errorf("ttl = %d, want %d", meta.TTL, wantMeta.TTL)
	}
	if meta.IfIndex != wantMeta.IfIndex {
		t.Errorf("ifindex = %d, want %d", meta.IfIndex, wantMeta.IfIndex)
	}
}

// TestMockPacketConnClose verifies that Close marks the connection as
// closed and subsequent operations return ErrSocketClosed.
func TestMockPacketConnClose(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	if err := mock.Close(); err != nil {
		t.Fatalf("close: unexpected error: %v", err)
	}

	buf := make([]byte, 64)
	_, _, err := mock.ReadPacket(buf)
	if !errors.Is(err, netio.ErrSocketClosed) {
		t.Errorf("read after close: got %v, want %v", err, netio.ErrSocketClosed)
	}

	dst := netip.MustParseAddr("10.0.0.1")
	err = mock.WritePacket([]byte{0x01}, dst)
	if !errors.Is(err, netio.ErrSocketClosed) {
		t.Errorf("write after close: got %v, want %v", err, netio.ErrSocketClosed)
	}
}

// TestMockPacketConnLocalAddr verifies LocalAddr returns the configured address.
func TestMockPacketConnLocalAddr(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("10.0.0.5:4784")
	mock := NewMockPacketConn(addr)

	if mock.LocalAddr() != addr {
		t.Errorf("LocalAddr = %s, want %s", mock.LocalAddr(), addr)
	}
}

// -------------------------------------------------------------------------
// Tests — PacketMeta Fields
// -------------------------------------------------------------------------

// TestPacketMetaFields verifies that PacketMeta correctly stores and
// returns all transport metadata fields.
func TestPacketMetaFields(t *testing.T) {
	t.Parallel()

	meta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("192.168.1.10"),
		DstAddr: netip.MustParseAddr("192.168.1.20"),
		TTL:     255,
		IfIndex: 42,
		IfName:  "eth0",
	}

	if meta.SrcAddr != netip.MustParseAddr("192.168.1.10") {
		t.Errorf("SrcAddr = %s, want 192.168.1.10", meta.SrcAddr)
	}
	if meta.DstAddr != netip.MustParseAddr("192.168.1.20") {
		t.Errorf("DstAddr = %s, want 192.168.1.20", meta.DstAddr)
	}
	if meta.TTL != 255 {
		t.Errorf("TTL = %d, want 255", meta.TTL)
	}
	if meta.IfIndex != 42 {
		t.Errorf("IfIndex = %d, want 42", meta.IfIndex)
	}
	if meta.IfName != "eth0" {
		t.Errorf("IfName = %s, want eth0", meta.IfName)
	}
}

// TestPacketMetaFieldsIPv6 verifies that PacketMeta correctly stores
// IPv6 addresses and hop limit (stored in TTL field).
func TestPacketMetaFieldsIPv6(t *testing.T) {
	t.Parallel()

	meta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("2001:db8::1"),
		DstAddr: netip.MustParseAddr("2001:db8::2"),
		TTL:     255,
		IfIndex: 7,
		IfName:  "eth1",
	}

	if meta.SrcAddr != netip.MustParseAddr("2001:db8::1") {
		t.Errorf("SrcAddr = %s, want 2001:db8::1", meta.SrcAddr)
	}
	if meta.DstAddr != netip.MustParseAddr("2001:db8::2") {
		t.Errorf("DstAddr = %s, want 2001:db8::2", meta.DstAddr)
	}
	if !meta.SrcAddr.Is6() {
		t.Error("SrcAddr should be IPv6")
	}
	if !meta.DstAddr.Is6() {
		t.Error("DstAddr should be IPv6")
	}
	if meta.TTL != 255 {
		t.Errorf("TTL (HopLimit) = %d, want 255", meta.TTL)
	}
	if meta.IfIndex != 7 {
		t.Errorf("IfIndex = %d, want 7", meta.IfIndex)
	}
	if meta.IfName != "eth1" {
		t.Errorf("IfName = %s, want eth1", meta.IfName)
	}
}

// TestPacketMetaZeroValue verifies that a zero-value PacketMeta has
// sensible defaults (zero addr, zero TTL, etc.).
func TestPacketMetaZeroValue(t *testing.T) {
	t.Parallel()

	var meta netio.PacketMeta

	if meta.SrcAddr.IsValid() {
		t.Error("zero-value SrcAddr should not be valid")
	}
	if meta.DstAddr.IsValid() {
		t.Error("zero-value DstAddr should not be valid")
	}
	if meta.TTL != 0 {
		t.Errorf("zero-value TTL = %d, want 0", meta.TTL)
	}
	if meta.IfIndex != 0 {
		t.Errorf("zero-value IfIndex = %d, want 0", meta.IfIndex)
	}
	if meta.IfName != "" {
		t.Errorf("zero-value IfName = %q, want empty", meta.IfName)
	}
}

// -------------------------------------------------------------------------
// Tests — Listener with Mock
// -------------------------------------------------------------------------

// TestListenerRecvWithMock verifies that Listener.Recv reads from the
// underlying PacketConn and validates TTL before returning.
func TestListenerRecvWithMock(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("192.168.1.1:3784")
	mock := NewMockPacketConn(addr)

	wantMeta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("10.0.0.2"),
		TTL:     255,
		IfIndex: 1,
		IfName:  "lo",
	}
	bfdData := []byte{0x20, 0x40, 0x03, 0x18}

	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		n := copy(buf, bfdData)
		return n, wantMeta, nil
	}

	listener := netio.NewListenerFromConn(mock, false)
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	buf, meta, err := listener.Recv(t.Context())
	if err != nil {
		t.Fatalf("recv: unexpected error: %v", err)
	}

	if len(buf) != len(bfdData) {
		t.Errorf("buf len = %d, want %d", len(buf), len(bfdData))
	}
	if meta.SrcAddr != wantMeta.SrcAddr {
		t.Errorf("src = %s, want %s", meta.SrcAddr, wantMeta.SrcAddr)
	}
	if meta.TTL != 255 {
		t.Errorf("ttl = %d, want 255", meta.TTL)
	}
}

// TestListenerRecvWithMockIPv6 verifies that Listener.Recv correctly
// handles IPv6 addresses and hop limit validation via mock.
func TestListenerRecvWithMockIPv6(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("[::1]:3784")
	mock := NewMockPacketConn(addr)

	wantMeta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("2001:db8::1"),
		DstAddr: netip.MustParseAddr("2001:db8::2"),
		TTL:     255,
		IfIndex: 2,
		IfName:  "eth0",
	}
	bfdData := []byte{0x20, 0x40, 0x03, 0x18}

	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		n := copy(buf, bfdData)
		return n, wantMeta, nil
	}

	listener := netio.NewListenerFromConn(mock, false)
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	buf, meta, err := listener.Recv(t.Context())
	if err != nil {
		t.Fatalf("recv: unexpected error: %v", err)
	}

	if len(buf) != len(bfdData) {
		t.Errorf("buf len = %d, want %d", len(buf), len(bfdData))
	}
	if meta.SrcAddr != wantMeta.SrcAddr {
		t.Errorf("src = %s, want %s", meta.SrcAddr, wantMeta.SrcAddr)
	}
	if !meta.SrcAddr.Is6() {
		t.Error("expected IPv6 source address")
	}
	if meta.TTL != 255 {
		t.Errorf("hop limit = %d, want 255", meta.TTL)
	}
}

// TestListenerRecvRejectsBadTTL verifies that the Listener drops packets
// with invalid TTL and continues reading until a valid packet arrives.
func TestListenerRecvRejectsBadTTL(t *testing.T) {
	t.Parallel()

	testListenerRecvRejectsBadTTL(
		t,
		netip.MustParseAddrPort("192.168.1.1:3784"),
		netip.MustParseAddr("10.0.0.2"),
		254,
		"TTL",
	)
}

func testListenerRecvRejectsBadTTL(
	t *testing.T,
	localAddr netip.AddrPort,
	srcAddr netip.Addr,
	invalidTTL uint8,
	valueName string,
) {
	t.Helper()

	mock := NewMockPacketConn(localAddr)

	callCount := 0
	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		callCount++
		data := []byte{0x20, 0x40, 0x03, 0x18}
		n := copy(buf, data)

		if callCount <= 2 {
			return n, netio.PacketMeta{
				SrcAddr: srcAddr,
				TTL:     invalidTTL,
			}, nil
		}

		return n, netio.PacketMeta{
			SrcAddr: srcAddr,
			TTL:     255,
		}, nil
	}

	listener := netio.NewListenerFromConn(mock, false)
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	_, meta, err := listener.Recv(t.Context())
	if err != nil {
		t.Fatalf("recv: unexpected error: %v", err)
	}

	if meta.TTL != 255 {
		t.Errorf("received packet with %s %d, expected 255", valueName, meta.TTL)
	}

	if callCount != 3 {
		t.Errorf("read count = %d, expected 3 (2 dropped + 1 valid)", callCount)
	}
}

// TestListenerRecvAcceptsEchoLoopbackTTL verifies that RFC 9747 echo listeners
// accept looped-back packets with TTL=254 and reject ordinary single-hop TTL=255.
func TestListenerRecvAcceptsEchoLoopbackTTL(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("192.168.1.1:3785")
	mock := NewMockPacketConn(addr)

	callCount := 0
	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		callCount++
		data := []byte{0x20, 0x40, 0x03, 0x18}
		n := copy(buf, data)

		if callCount == 1 {
			return n, netio.PacketMeta{
				SrcAddr: netip.MustParseAddr("10.0.0.2"),
				TTL:     255,
			}, nil
		}

		return n, netio.PacketMeta{
			SrcAddr: netip.MustParseAddr("10.0.0.2"),
			TTL:     254,
		}, nil
	}

	listener := netio.NewListenerFromConnWithExpectedTTL(mock, false, netio.TTLEchoLoopback)
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	_, meta, err := listener.Recv(t.Context())
	if err != nil {
		t.Fatalf("recv: unexpected error: %v", err)
	}

	if meta.TTL != netio.TTLEchoLoopback {
		t.Errorf("received packet with TTL %d, expected %d", meta.TTL, netio.TTLEchoLoopback)
	}
	if callCount != 2 {
		t.Errorf("read count = %d, expected 2 (1 dropped + 1 valid)", callCount)
	}
}

// TestListenerRecvRejectsBadHopLimitIPv6 verifies that the Listener drops
// IPv6 packets with invalid Hop Limit (stored in TTL field) and continues
// reading until a valid packet arrives.
func TestListenerRecvRejectsBadHopLimitIPv6(t *testing.T) {
	t.Parallel()

	testListenerRecvRejectsBadTTL(
		t,
		netip.MustParseAddrPort("[::1]:3784"),
		netip.MustParseAddr("2001:db8::1"),
		64,
		"HopLimit",
	)
}

// TestMockPacketConnWriteIPv6 verifies that WritePacket records IPv6
// destination addresses correctly.
func TestMockPacketConnWriteIPv6(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("[::1]:3784")
	mock := NewMockPacketConn(addr)

	dst := netip.MustParseAddr("2001:db8::1")
	payload := []byte{0x20, 0x40, 0x03, 0x18, 0x00, 0x00, 0x00, 0x01}

	err := mock.WritePacket(payload, dst)
	if err != nil {
		t.Fatalf("write: unexpected error: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.Written) != 1 {
		t.Fatalf("expected 1 written packet, got %d", len(mock.Written))
	}

	if mock.Written[0].Dst != dst {
		t.Errorf("dst = %s, want %s", mock.Written[0].Dst, dst)
	}

	if !mock.Written[0].Dst.Is6() {
		t.Error("expected IPv6 destination address")
	}

	if len(mock.Written[0].Data) != len(payload) {
		t.Errorf("data length = %d, want %d", len(mock.Written[0].Data), len(payload))
	}
}

// TestMockPacketConnReadIPv6 verifies that ReadPacket correctly returns
// IPv6 metadata from the injected ReadFunc.
func TestMockPacketConnReadIPv6(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("[::1]:3784")
	mock := NewMockPacketConn(addr)

	wantMeta := netio.PacketMeta{
		SrcAddr: netip.MustParseAddr("2001:db8::1"),
		DstAddr: netip.MustParseAddr("2001:db8::2"),
		TTL:     255,
		IfIndex: 5,
		IfName:  "eth0",
	}
	wantData := []byte{0x20, 0x40, 0x03, 0x18}

	mock.ReadFunc = func(buf []byte) (int, netio.PacketMeta, error) {
		n := copy(buf, wantData)
		return n, wantMeta, nil
	}

	buf := make([]byte, 64)
	n, meta, err := mock.ReadPacket(buf)
	if err != nil {
		t.Fatalf("read: unexpected error: %v", err)
	}

	if n != len(wantData) {
		t.Errorf("n = %d, want %d", n, len(wantData))
	}
	if meta.SrcAddr != wantMeta.SrcAddr {
		t.Errorf("src = %s, want %s", meta.SrcAddr, wantMeta.SrcAddr)
	}
	if !meta.SrcAddr.Is6() {
		t.Error("expected IPv6 source address")
	}
	if meta.DstAddr != wantMeta.DstAddr {
		t.Errorf("dst = %s, want %s", meta.DstAddr, wantMeta.DstAddr)
	}
	if meta.TTL != wantMeta.TTL {
		t.Errorf("hop limit = %d, want %d", meta.TTL, wantMeta.TTL)
	}
	if meta.IfIndex != wantMeta.IfIndex {
		t.Errorf("ifindex = %d, want %d", meta.IfIndex, wantMeta.IfIndex)
	}
}

// TestMockPacketConnLocalAddrIPv6 verifies LocalAddr returns the
// configured IPv6 address.
func TestMockPacketConnLocalAddrIPv6(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("[2001:db8::1]:4784")
	mock := NewMockPacketConn(addr)

	if mock.LocalAddr() != addr {
		t.Errorf("LocalAddr = %s, want %s", mock.LocalAddr(), addr)
	}
}

// -------------------------------------------------------------------------
// Tests — Constants
// -------------------------------------------------------------------------

// TestBFDPortConstants verifies the well-known BFD port numbers match
// their RFC-defined values.
func TestBFDPortConstants(t *testing.T) {
	t.Parallel()

	if netio.PortSingleHop != 3784 {
		t.Errorf("PortSingleHop = %d, want 3784 (RFC 5881 Section 4)", netio.PortSingleHop)
	}
	if netio.PortMultiHop != 4784 {
		t.Errorf("PortMultiHop = %d, want 4784 (RFC 5883 Section 2)", netio.PortMultiHop)
	}
}

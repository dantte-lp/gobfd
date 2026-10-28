package netio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinkRequestEncodesSetMaster(t *testing.T) {
	t.Parallel()

	value := binary.NativeEndian.AppendUint32(nil, 10)
	got := linkRequest(unix.RTM_NEWLINK, 20, rtattr(unix.IFLA_MASTER, value))

	want := make([]byte, 0, 40)
	want = binary.NativeEndian.AppendUint32(want, 40)
	want = binary.NativeEndian.AppendUint16(want, unix.RTM_NEWLINK)
	want = binary.NativeEndian.AppendUint16(want, unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	want = binary.NativeEndian.AppendUint32(want, rtnetlinkSeq)
	want = binary.NativeEndian.AppendUint32(want, 0) // nlmsg_pid
	want = append(want, unix.AF_UNSPEC, 0, 0, 0)
	want = binary.NativeEndian.AppendUint32(want, 20) // ifi_index
	want = binary.NativeEndian.AppendUint32(want, 0)  // ifi_flags
	want = binary.NativeEndian.AppendUint32(want, 0)  // ifi_change
	want = binary.NativeEndian.AppendUint16(want, 8)
	want = binary.NativeEndian.AppendUint16(want, unix.IFLA_MASTER)
	want = append(want, value...)
	if !bytes.Equal(got, want) {
		t.Fatalf("request = % x\nwant      % x", got, want)
	}
}

func TestParseKernelBondLinkReadsMasterAndKind(t *testing.T) {
	t.Parallel()

	ifi := make([]byte, unix.SizeofIfInfomsg)
	binary.NativeEndian.PutUint32(ifi[4:8], 7)
	info := append(rtattr(unix.IFLA_INFO_KIND, []byte("bond\x00")), rtattr(unix.IFLA_INFO_DATA, []byte{1, 2, 3, 4})...)
	body := append(ifi, rtattr(unix.IFLA_IFNAME, []byte("gobfdm0a\x00"))...)
	body = append(body, rtattr(unix.IFLA_MASTER, binary.NativeEndian.AppendUint32(nil, 3))...)
	body = append(body, rtattr(unix.IFLA_LINKINFO|unix.NLA_F_NESTED, info)...)

	got, err := parseKernelBondLink(body)
	if err != nil {
		t.Fatalf("parseKernelBondLink: %v", err)
	}
	if want := (KernelBondLink{Index: 7, Kind: "bond", MasterIndex: 3}); got != want {
		t.Fatalf("link = %+v, want %+v", got, want)
	}
}

func TestParseKernelBondLinkRejectsMalformedAttributes(t *testing.T) {
	t.Parallel()

	ifi := make([]byte, unix.SizeofIfInfomsg)
	for name, body := range map[string][]byte{
		"short ifinfomsg":  ifi[:8],
		"overlong attr":    append(append([]byte(nil), ifi...), 0x20, 0x00, unix.IFLA_MASTER, 0x00),
		"short master":     append(append([]byte(nil), ifi...), rtattr(unix.IFLA_MASTER, []byte{1})...),
		"undersized attr":  append(append([]byte(nil), ifi...), 0x02, 0x00, 0x00, 0x00),
		"overlong nested":  append(append([]byte(nil), ifi...), rtattr(unix.IFLA_LINKINFO, []byte{0x40, 0, 1, 0})...),
	} {
		if _, err := parseKernelBondLink(body); !errors.Is(err, errMalformedRTNetlink) {
			t.Errorf("%s: error = %v, want errMalformedRTNetlink", name, err)
		}
	}
}

func TestRTNetlinkAckErrorMapsErrno(t *testing.T) {
	t.Parallel()

	if err := rtnetlinkAckError(binary.NativeEndian.AppendUint32(nil, 0)); err != nil {
		t.Fatalf("ACK 0 = %v, want nil", err)
	}
	enodev := uint32(unix.ENODEV)
	if err := rtnetlinkAckError(binary.NativeEndian.AppendUint32(nil, -enodev)); !errors.Is(err, unix.ENODEV) {
		t.Fatalf("ACK -ENODEV = %v, want ENODEV", err)
	}
	if err := rtnetlinkAckError([]byte{0}); !errors.Is(err, errMalformedRTNetlink) {
		t.Fatalf("short ACK = %v, want errMalformedRTNetlink", err)
	}
}

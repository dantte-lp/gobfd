//go:build interop_clab

package interop_clab_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

const (
	wirePCAPHeader       = 24
	wirePCAPRecord       = 16
	wirePCAPRecords      = 20
	wirePCAPSnaplen      = 256
	wirePCAPMaxBytes     = wirePCAPHeader + wirePCAPRecords*(wirePCAPRecord+wirePCAPSnaplen)
	wirePCAPMicroMagic   = 0xa1b2c3d4
	wirePCAPMicroSwapped = 0xd4c3b2a1
	wirePCAPNanoMagic    = 0xa1b23c4d
	wirePCAPNanoSwapped  = 0x4d3cb2a1
	wireMicrosecondLimit = 1000000
	wireNanosecondLimit  = 1000000000
	wirePCAPMajor        = 2
	wirePCAPMinor        = 4
	wireEthernetLink     = 1
	wireEthernetHeader   = 14
	wireEtherTypeIPv4    = 0x0800
	wireEtherTypeIPv6    = 0x86dd
	wireIPv4Header       = 20
	wireIPv6Header       = 40
	wireIPv4Version      = 4
	wireIPv6Version      = 6
	wireIPVersionShift   = 4
	wireIPv4IHLMask      = 0x0f
	wireIPv4WordBytes    = 4
	wireIPv4Fragments    = 0x3fff
	wireUDPProtocol      = 17
	wireIPHopLimit       = 255
	wireUDPHeader        = 8
	wireSourcePortMin    = 49152
	wireDestinationPort  = 3784
	wireBFDHeader        = 24
	wireBFDVersionShift  = 5
	wireBFDVersion       = 1
)

const (
	wireIPv4Family = iota
	wireIPv6Family
	wireFamilyCount
)

// validateFRRWireCapture checks the bounded Ethernet capture from the FRR link.
func validateFRRWireCapture(data []byte) error {
	if len(data) < wirePCAPHeader || len(data) > wirePCAPMaxBytes {
		return fmt.Errorf("PCAP input length %d outside controlled capture bounds", len(data))
	}
	order, fractionLimit, err := readFRRWireHeader(data[:wirePCAPHeader])
	if err != nil {
		return err
	}
	data = data[wirePCAPHeader:]
	var ports [wireFamilyCount]uint16
	for index := range wirePCAPRecords {
		frame, rest, recordErr := readFRRWireRecord(data, order, fractionLimit)
		if recordErr != nil {
			return fmt.Errorf("PCAP record %d: %w", index, recordErr)
		}
		family, port, frameErr := inspectFRRWireFrame(frame)
		if frameErr != nil {
			return fmt.Errorf("PCAP record %d: %w", index, frameErr)
		}
		if ports[family] != 0 && ports[family] != port {
			return fmt.Errorf("PCAP record %d: source port changed from %d to %d", index, ports[family], port)
		}
		ports[family], data = port, rest
	}
	if len(data) != 0 {
		return errors.New("PCAP contains records or trailing bytes after the required twenty frames")
	}
	if ports[wireIPv4Family] == 0 || ports[wireIPv6Family] == 0 {
		return errors.New("PCAP must contain both GoBFD-to-FRR IPv4 and IPv6 tuples")
	}
	return nil
}

func readFRRWireHeader(header []byte) (binary.ByteOrder, uint32, error) {
	order, fractionLimit, err := frrWireByteOrder(binary.BigEndian.Uint32(header[:4]))
	if err != nil {
		return nil, 0, err
	}
	if order.Uint16(header[4:6]) != wirePCAPMajor || order.Uint16(header[6:8]) != wirePCAPMinor {
		return nil, 0, errors.New("PCAP version must be 2.4")
	}
	if order.Uint32(header[16:20]) != wirePCAPSnaplen {
		return nil, 0, errors.New("PCAP snaplen must be 256")
	}
	if order.Uint32(header[20:24]) != wireEthernetLink {
		return nil, 0, errors.New("PCAP linktype must be Ethernet without additional flags")
	}
	return order, fractionLimit, nil
}

func frrWireByteOrder(magic uint32) (binary.ByteOrder, uint32, error) {
	switch magic {
	case wirePCAPMicroMagic:
		return binary.BigEndian, wireMicrosecondLimit, nil
	case wirePCAPMicroSwapped:
		return binary.LittleEndian, wireMicrosecondLimit, nil
	case wirePCAPNanoMagic:
		return binary.BigEndian, wireNanosecondLimit, nil
	case wirePCAPNanoSwapped:
		return binary.LittleEndian, wireNanosecondLimit, nil
	default:
		return nil, 0, errors.New("unsupported classic PCAP magic")
	}
}

func readFRRWireRecord(data []byte, order binary.ByteOrder, fractionLimit uint32) ([]byte, []byte, error) {
	if len(data) < wirePCAPRecord {
		return nil, nil, errors.New("missing or truncated PCAP record header")
	}
	if order.Uint32(data[4:8]) >= fractionLimit {
		return nil, nil, errors.New("PCAP timestamp fraction exceeds selected precision")
	}
	captured, original := order.Uint32(data[8:12]), order.Uint32(data[12:16])
	if captured == 0 || captured > wirePCAPSnaplen || captured != original {
		return nil, nil, errors.New("PCAP record is empty, exceeds snaplen, or is truncated")
	}
	data = data[wirePCAPRecord:]
	if uint64(captured) > uint64(len(data)) {
		return nil, nil, errors.New("truncated PCAP record body")
	}
	// captured is bounded by snaplen and the available body before conversion.
	length := int(captured)
	return data[:length], data[length:], nil
}

func inspectFRRWireFrame(frame []byte) (int, uint16, error) {
	if len(frame) < wireEthernetHeader {
		return 0, 0, errors.New("truncated Ethernet header")
	}
	var (
		family int
		udp    []byte
		err    error
	)
	switch binary.BigEndian.Uint16(frame[12:14]) {
	case wireEtherTypeIPv4:
		family = wireIPv4Family
		udp, err = readFRRWireIPv4(frame[wireEthernetHeader:])
	case wireEtherTypeIPv6:
		family = wireIPv6Family
		udp, err = readFRRWireIPv6(frame[wireEthernetHeader:])
	default:
		return 0, 0, errors.New("unsupported Ethernet protocol in FRR capture")
	}
	if err != nil {
		return 0, 0, err
	}
	port, err := validateFRRWireUDP(udp)
	return family, port, err
}

func readFRRWireIPv4(ip []byte) ([]byte, error) {
	if len(ip) < wireIPv4Header || ip[0]>>wireIPVersionShift != wireIPv4Version {
		return nil, errors.New("invalid or truncated IPv4 header")
	}
	headerLength := int(ip[0]&wireIPv4IHLMask) * wireIPv4WordBytes
	totalLength := int(binary.BigEndian.Uint16(ip[2:4]))
	if headerLength < wireIPv4Header || headerLength > len(ip) ||
		totalLength < headerLength+wireUDPHeader || totalLength > len(ip) {
		return nil, errors.New("IPv4 header or total length exceeds packet bounds")
	}
	if binary.BigEndian.Uint16(ip[6:8])&wireIPv4Fragments != 0 || ip[9] != wireUDPProtocol {
		return nil, errors.New("FRR IPv4 capture requires unfragmented UDP")
	}
	if ip[8] != wireIPHopLimit {
		return nil, fmt.Errorf("IPv4 TTL is %d, require 255", ip[8])
	}
	source := netip.AddrFrom4([4]byte(ip[12:16]))
	destination := netip.AddrFrom4([4]byte(ip[16:20]))
	if source != netip.AddrFrom4([4]byte{10, 0, 6, 1}) ||
		destination != netip.AddrFrom4([4]byte{10, 0, 6, 2}) {
		return nil, fmt.Errorf("unexpected IPv4 tuple %s -> %s", source, destination)
	}
	return ip[headerLength:totalLength], nil
}

func readFRRWireIPv6(ip []byte) ([]byte, error) {
	if len(ip) < wireIPv6Header || ip[0]>>wireIPVersionShift != wireIPv6Version {
		return nil, errors.New("invalid or truncated IPv6 header")
	}
	payloadLength := int(binary.BigEndian.Uint16(ip[4:6]))
	if payloadLength < wireUDPHeader || payloadLength > len(ip)-wireIPv6Header {
		return nil, errors.New("IPv6 payload length exceeds packet bounds")
	}
	if ip[6] != wireUDPProtocol {
		return nil, errors.New("FRR IPv6 capture requires UDP without extension or fragment headers")
	}
	if ip[7] != wireIPHopLimit {
		return nil, fmt.Errorf("IPv6 Hop Limit is %d, require 255", ip[7])
	}
	source := netip.AddrFrom16([16]byte(ip[8:24]))
	destination := netip.AddrFrom16([16]byte(ip[24:40]))
	if source != netip.MustParseAddr("fd00:0:6::") || destination != netip.MustParseAddr("fd00:0:6::1") {
		return nil, fmt.Errorf("unexpected IPv6 tuple %s -> %s", source, destination)
	}
	return ip[wireIPv6Header : wireIPv6Header+payloadLength], nil
}

func validateFRRWireUDP(udp []byte) (uint16, error) {
	if len(udp) < wireUDPHeader+wireBFDHeader {
		return 0, errors.New("truncated UDP/BFD packet")
	}
	source, destination := binary.BigEndian.Uint16(udp[:2]), binary.BigEndian.Uint16(udp[2:4])
	if source < wireSourcePortMin || destination != wireDestinationPort {
		return 0, fmt.Errorf("invalid single-hop BFD UDP ports %d -> %d", source, destination)
	}
	if int(binary.BigEndian.Uint16(udp[4:6])) != len(udp) {
		return 0, errors.New("UDP length does not match IP payload length")
	}
	bfd := udp[wireUDPHeader:]
	if bfd[0]>>wireBFDVersionShift != wireBFDVersion {
		return 0, fmt.Errorf("BFD version is %d, require 1", bfd[0]>>wireBFDVersionShift)
	}
	if int(bfd[3]) < wireBFDHeader || int(bfd[3]) > len(bfd) {
		return 0, errors.New("declared BFD length exceeds UDP payload or is shorter than the fixed header")
	}
	return source, nil
}

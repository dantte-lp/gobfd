package main

import (
	"encoding/binary"
	"fmt"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

const defaultTTL = 255

const (
	xorShiftLeftFirst  = 13
	xorShiftRight      = 7
	xorShiftLeftSecond = 17
	discriminatorEnd   = 8
	lowByteMask        = 0xff
)

type vectorPacket struct {
	Detail  string
	Payload []byte
	TTL     int
}

type vectorCase struct {
	Name    string
	Packets []vectorPacket
}

func invalidVectorCases() ([]vectorCase, error) {
	builders := []func() (vectorCase, error){
		invalidVersionCase,
		zeroDetectMultiplierCase,
		multipointCase,
		zeroMyDiscriminatorCase,
		shortLengthCase,
		lengthExceedsPayloadCase,
		truncatedPacketCase,
		authFlagWithoutSectionCase,
		truncatedAuthSectionCase,
		invalidAuthTypeCase,
		allFlagsCase,
		maximumFieldsCase,
		zeroIntervalsCase,
		zeroYourDiscriminatorCase,
		randomGarbageCase,
		oversizedPacketCase,
		wrongTTLCase,
		rapidFireCase,
	}

	cases := make([]vectorCase, 0, len(builders))
	for _, build := range builders {
		testCase, err := build()
		if err != nil {
			return nil, err
		}
		cases = append(cases, testCase)
	}
	return cases, nil
}

func invalidVersionCase() (vectorCase, error) {
	testCase := vectorCase{Name: "invalid_version"}
	for _, version := range []uint8{0, 2, 3, 4, 5, 6, 7} {
		packet := defaultControlPacket(0xF0F00001)
		packet.Version = version
		if err := testCase.appendControl(fmt.Sprintf("version=%d", version), packet, defaultTTL); err != nil {
			return vectorCase{}, err
		}
	}
	return testCase, nil
}

func zeroDetectMultiplierCase() (vectorCase, error) {
	packet := defaultControlPacket(0xF0F00002)
	packet.DetectMult = 0
	return singleControlCase("zero_detect_mult", "detect_mult=0", packet)
}

func multipointCase() (vectorCase, error) {
	packet := defaultControlPacket(0xF0F00003)
	packet.Multipoint = true
	return singleControlCase("multipoint_set", "M=1", packet)
}

func zeroMyDiscriminatorCase() (vectorCase, error) {
	return singleControlCase("zero_my_discriminator", "my_discr=0", defaultControlPacket(0))
}

func shortLengthCase() (vectorCase, error) {
	testCase := vectorCase{Name: "length_too_small"}
	for _, length := range []byte{0, 1, 12, 23} {
		payload, err := marshalControl(defaultControlPacket(0xF0F00004))
		if err != nil {
			return vectorCase{}, err
		}
		payload[3] = length
		testCase.appendRaw(fmt.Sprintf("length=%d", length), payload, defaultTTL)
	}
	return testCase, nil
}

func lengthExceedsPayloadCase() (vectorCase, error) {
	payload, err := marshalControl(defaultControlPacket(0xF0F00005))
	if err != nil {
		return vectorCase{}, err
	}
	payload[3] = 48
	return singleRawCase("length_exceeds_payload", "length=48, actual=24", payload, defaultTTL), nil
}

func truncatedPacketCase() (vectorCase, error) {
	payload, err := marshalControl(defaultControlPacket(0xF0F00006))
	if err != nil {
		return vectorCase{}, err
	}
	testCase := vectorCase{Name: "truncated_packet"}
	for _, size := range []int{0, 1, 4, 12, 20, 23} {
		testCase.appendRaw(fmt.Sprintf("size=%d", size), payload[:size], defaultTTL)
	}
	return testCase, nil
}

func authFlagWithoutSectionCase() (vectorCase, error) {
	packet := defaultControlPacket(0xF0F00007)
	packet.AuthPresent = true
	return singleControlCase("auth_flag_no_section", "A=1, length=24, no_auth_data", packet)
}

func truncatedAuthSectionCase() (vectorCase, error) {
	testCase := vectorCase{Name: "auth_section_truncated"}
	payload, err := rawAuthPacket(0xF0F00008, 26, []byte{1})
	if err != nil {
		return vectorCase{}, err
	}
	testCase.appendRaw("auth_data=1_byte", payload, defaultTTL)
	payload, err = rawAuthPacket(0xF0F00009, 27, []byte{4, 28, 1})
	if err != nil {
		return vectorCase{}, err
	}
	testCase.appendRaw("sha1_auth_truncated", payload, defaultTTL)
	return testCase, nil
}

func invalidAuthTypeCase() (vectorCase, error) {
	testCase := vectorCase{Name: "invalid_auth_type"}
	for _, authType := range []byte{6, 7, 128, 255} {
		payload, err := rawAuthPacket(0xF0F0000A, 27, []byte{authType, 3, 1})
		if err != nil {
			return vectorCase{}, err
		}
		testCase.appendRaw(fmt.Sprintf("auth_type=%d", authType), payload, defaultTTL)
	}
	return testCase, nil
}

func allFlagsCase() (vectorCase, error) {
	packet := defaultControlPacket(0xF0F0000B)
	packet.Poll = true
	packet.Final = true
	packet.ControlPlaneIndependent = true
	packet.AuthPresent = true
	packet.Demand = true
	packet.Multipoint = true
	return singleControlCase("all_flags_set", "P=F=C=A=D=M=1", packet)
}

func maximumFieldsCase() (vectorCase, error) {
	testCase := vectorCase{Name: "max_field_values"}
	packet := defaultControlPacket(0xF0F0000C)
	packet.DetectMult = 255
	if err := testCase.appendControl("detect_mult=255", packet, defaultTTL); err != nil {
		return vectorCase{}, err
	}
	packet = defaultControlPacket(^uint32(0))
	packet.YourDiscriminator = ^uint32(0)
	if err := testCase.appendControl("max_discriminators", packet, defaultTTL); err != nil {
		return vectorCase{}, err
	}
	packet = defaultControlPacket(0xF0F0000D)
	packet.DesiredMinTxInterval = ^uint32(0)
	packet.RequiredMinRxInterval = ^uint32(0)
	packet.RequiredMinEchoRxInterval = ^uint32(0)
	if err := testCase.appendControl("max_intervals", packet, defaultTTL); err != nil {
		return vectorCase{}, err
	}
	return testCase, nil
}

func zeroIntervalsCase() (vectorCase, error) {
	testCase := vectorCase{Name: "zero_intervals"}
	packet := defaultControlPacket(0xF0F0000E)
	packet.DesiredMinTxInterval = 0
	if err := testCase.appendControl("desired_min_tx=0", packet, defaultTTL); err != nil {
		return vectorCase{}, err
	}
	packet = defaultControlPacket(0xF0F0000F)
	packet.RequiredMinRxInterval = 0
	if err := testCase.appendControl("required_min_rx=0", packet, defaultTTL); err != nil {
		return vectorCase{}, err
	}
	return testCase, nil
}

func zeroYourDiscriminatorCase() (vectorCase, error) {
	testCase := vectorCase{Name: "your_discr_zero_non_down"}
	for _, state := range []bfd.State{bfd.StateInit, bfd.StateUp} {
		packet := defaultControlPacket(0xF0F00012)
		packet.State = state
		if err := testCase.appendControl("state="+state.String()+",your_discr=0", packet, defaultTTL); err != nil {
			return vectorCase{}, err
		}
	}
	return testCase, nil
}

func randomGarbageCase() (vectorCase, error) {
	testCase := vectorCase{Name: "random_garbage"}
	generator := newDeterministicBytes(42)
	for _, size := range []int{1, 8, 24, 32, 48, 64, 128, 256, 512, 1024} {
		payload := make([]byte, size)
		generator.fill(payload)
		testCase.appendRaw(fmt.Sprintf("random_%dbytes", size), payload, defaultTTL)
	}
	return testCase, nil
}

func oversizedPacketCase() (vectorCase, error) {
	base, err := marshalControl(defaultControlPacket(0xF0F00010))
	if err != nil {
		return vectorCase{}, err
	}
	testCase := vectorCase{Name: "oversized_packet"}
	for _, extra := range []int{1, 16, 64, 256, 1024} {
		payload := append(append([]byte(nil), base...), make([]byte, extra)...)
		testCase.appendRaw(fmt.Sprintf("size=%d", len(payload)), payload, defaultTTL)
	}
	return testCase, nil
}

func wrongTTLCase() (vectorCase, error) {
	testCase := vectorCase{Name: "wrong_ttl"}
	for _, ttl := range []int{1, 64, 128, 254} {
		if err := testCase.appendControl(fmt.Sprintf("ttl=%d", ttl), defaultControlPacket(0xF0F00011), ttl); err != nil {
			return vectorCase{}, err
		}
	}
	return testCase, nil
}

func rapidFireCase() (vectorCase, error) {
	testCase := vectorCase{Name: "rapid_fire", Packets: make([]vectorPacket, 0, 1000)}
	for index := range 1000 {
		packet := defaultControlPacket(0xF0F10000 + uint32(index))
		packet.Version = uint8(index % 8)
		packet.DetectMult = uint8(index % 256)
		if err := testCase.appendControl(fmt.Sprintf("mixed_packet=%d", index), packet, defaultTTL); err != nil {
			return vectorCase{}, err
		}
	}
	return testCase, nil
}

func validProbePacket() (vectorPacket, error) {
	payload, err := marshalControl(defaultControlPacket(0xDEADDEAD))
	if err != nil {
		return vectorPacket{}, err
	}
	return vectorPacket{Detail: "valid_probe", Payload: payload, TTL: defaultTTL}, nil
}

func defaultControlPacket(discriminator uint32) bfd.ControlPacket {
	return bfd.ControlPacket{
		Version:               bfd.Version,
		State:                 bfd.StateDown,
		DetectMult:            3,
		MyDiscriminator:       discriminator,
		DesiredMinTxInterval:  1_000_000,
		RequiredMinRxInterval: 1_000_000,
	}
}

func singleControlCase(name, detail string, packet bfd.ControlPacket) (vectorCase, error) {
	testCase := vectorCase{Name: name}
	if err := testCase.appendControl(detail, packet, defaultTTL); err != nil {
		return vectorCase{}, err
	}
	return testCase, nil
}

func singleRawCase(name, detail string, payload []byte, ttl int) vectorCase {
	testCase := vectorCase{Name: name}
	testCase.appendRaw(detail, payload, ttl)
	return testCase
}

func (testCase *vectorCase) appendControl(detail string, packet bfd.ControlPacket, ttl int) error {
	payload, err := marshalControl(packet)
	if err != nil {
		return fmt.Errorf("build %s vector %s: %w", testCase.Name, detail, err)
	}
	testCase.appendRaw(detail, payload, ttl)
	return nil
}

func (testCase *vectorCase) appendRaw(detail string, payload []byte, ttl int) {
	testCase.Packets = append(testCase.Packets, vectorPacket{
		Detail: detail, Payload: append([]byte(nil), payload...), TTL: ttl,
	})
}

func marshalControl(packet bfd.ControlPacket) ([]byte, error) {
	buffer := make([]byte, bfd.MaxPacketSize)
	size, err := bfd.MarshalControlPacket(&packet, buffer)
	if err != nil {
		return nil, fmt.Errorf("marshal repository BFD control packet: %w", err)
	}
	return append([]byte(nil), buffer[:size]...), nil
}

func rawAuthPacket(discriminator uint32, length byte, auth []byte) ([]byte, error) {
	packet := defaultControlPacket(discriminator)
	packet.AuthPresent = true
	payload, err := marshalControl(packet)
	if err != nil {
		return nil, err
	}
	payload[3] = length
	return append(payload, auth...), nil
}

type deterministicBytes struct {
	state uint64
}

func newDeterministicBytes(seed uint64) *deterministicBytes {
	return &deterministicBytes{state: seed}
}

func (generator *deterministicBytes) fill(target []byte) {
	for index := range target {
		generator.state ^= generator.state << xorShiftLeftFirst
		generator.state ^= generator.state >> xorShiftRight
		generator.state ^= generator.state << xorShiftLeftSecond
		target[index] = byte(generator.state & lowByteMask)
	}
}

func discriminator(payload []byte) uint32 {
	if len(payload) < discriminatorEnd {
		return 0
	}
	return binary.BigEndian.Uint32(payload[4:8])
}

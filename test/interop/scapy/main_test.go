package main

import (
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestInvalidVectorContract(t *testing.T) {
	t.Parallel()

	cases, err := invalidVectorCases()
	if err != nil {
		t.Fatalf("invalidVectorCases: %v", err)
	}
	wantCounts := map[string]int{
		"invalid_version": 7, "zero_detect_mult": 1, "multipoint_set": 1,
		"zero_my_discriminator": 1, "length_too_small": 4, "length_exceeds_payload": 1,
		"truncated_packet": 6, "auth_flag_no_section": 1, "auth_section_truncated": 2,
		"invalid_auth_type": 4, "all_flags_set": 1, "max_field_values": 3,
		"zero_intervals": 2, "your_discr_zero_non_down": 2, "random_garbage": 10,
		"oversized_packet": 5, "wrong_ttl": 4, "rapid_fire": 1000,
	}
	packetCount := 0
	for _, testCase := range cases {
		want, ok := wantCounts[testCase.Name]
		if !ok {
			t.Fatalf("unexpected vector group %q", testCase.Name)
		}
		if len(testCase.Packets) != want {
			t.Fatalf("%s packet count = %d, want %d", testCase.Name, len(testCase.Packets), want)
		}
		delete(wantCounts, testCase.Name)
		packetCount += len(testCase.Packets)
	}
	if len(wantCounts) != 0 {
		t.Fatalf("missing vector groups: %v", wantCounts)
	}
	if packetCount != 1055 {
		t.Fatalf("invalid packet count = %d, want 1055", packetCount)
	}

	assertVectorError(t, cases, "invalid_version", bfd.ErrInvalidVersion)
	assertVectorError(t, cases, "zero_detect_mult", bfd.ErrZeroDetectMult)
	assertVectorError(t, cases, "multipoint_set", bfd.ErrMultipointSet)
	assertVectorError(t, cases, "zero_my_discriminator", bfd.ErrZeroMyDiscriminator)
	assertVectorError(t, cases, "length_too_small", bfd.ErrInvalidLength)
	assertVectorError(t, cases, "length_exceeds_payload", bfd.ErrLengthExceedsPayload)
	assertVectorError(t, cases, "truncated_packet", bfd.ErrPacketTooShort)
	assertVectorError(t, cases, "auth_flag_no_section", bfd.ErrInvalidLength)
	assertVectorError(t, cases, "auth_section_truncated", bfd.ErrLengthExceedsPayload)
	assertVectorError(t, cases, "invalid_auth_type", bfd.ErrInvalidAuthType)
	assertVectorError(t, cases, "your_discr_zero_non_down", bfd.ErrZeroYourDiscriminator)

	probe, err := validProbePacket()
	if err != nil {
		t.Fatalf("validProbePacket: %v", err)
	}
	var decoded bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(probe.Payload, &decoded); err != nil {
		t.Fatalf("valid probe rejected by repository codec: %v", err)
	}
}

func assertVectorError(t *testing.T, cases []vectorCase, name string, want error) {
	t.Helper()
	for _, testCase := range cases {
		if testCase.Name != name {
			continue
		}
		var decoded bfd.ControlPacket
		err := bfd.UnmarshalControlPacket(testCase.Packets[0].Payload, &decoded)
		if !errors.Is(err, want) {
			t.Fatalf("%s codec error = %v, want errors.Is(%v)", name, err, want)
		}
		return
	}
	t.Fatalf("vector group %s not found", name)
}

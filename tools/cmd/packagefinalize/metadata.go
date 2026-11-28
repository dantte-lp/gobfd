package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const (
	rpmLeadSize         = 96
	rpmHeaderSize       = 16
	rpmIndexSize        = 16
	rpmHeaderAlignment  = 8
	maxRPMHeaderEntries = 4096
	maxRPMHeaderStore   = 1 << 20
)

func inspectRPMMetadata(reader io.ReaderAt, version, arch string) error {
	var lead [rpmLeadSize]byte
	if _, err := reader.ReadAt(lead[:], 0); err != nil {
		return fmt.Errorf("%w: read RPM lead: %w", errInvalidPackageInput, err)
	}
	if string(lead[:4]) != "\xed\xab\xee\xdb" {
		return fmt.Errorf("%w: invalid RPM lead magic", errInvalidPackageInput)
	}
	_, signatureEnd, err := readRPMMetadataHeader(reader, rpmLeadSize)
	if err != nil {
		return err
	}
	mainOffset := (signatureEnd + rpmHeaderAlignment - 1) / rpmHeaderAlignment * rpmHeaderAlignment
	header, _, err := readRPMMetadataHeader(reader, mainOffset)
	if err != nil {
		return err
	}
	rpmArch := map[string]string{amd64: "x86_64", arm64: "aarch64"}[arch]
	if rpmArch == "" {
		return fmt.Errorf("%w: unsupported RPM metadata architecture %q", errInvalidPackageInput, arch)
	}
	expected := map[uint32]string{
		1000: "gobfd", 1001: strings.Replace(version, "-alpha.", "~alpha.", 1), 1002: "1", 1022: rpmArch,
		1024: "\n", 1025: "\n", postInstallProgramTag: postInstallProgram, preRemoveProgramTag: preRemoveProgram,
	}
	entries := binary.BigEndian.Uint32(header[8:12])
	store := header[rpmHeaderSize+entries*rpmIndexSize:]
	seen := make(map[uint32]bool, len(expected))
	for index := range entries {
		entry := header[rpmHeaderSize+index*rpmIndexSize:][:rpmIndexSize]
		tag := binary.BigEndian.Uint32(entry[:4])
		want, required := expected[tag]
		if !required {
			continue
		}
		kind := binary.BigEndian.Uint32(entry[4:8])
		offset := binary.BigEndian.Uint32(entry[8:12])
		count := binary.BigEndian.Uint32(entry[12:16])
		program := tag == postInstallProgramTag || tag == preRemoveProgramTag
		if seen[tag] || count != 1 || (kind != 6 && !(program && kind == 8)) || uint64(offset) >= uint64(len(store)) {
			return fmt.Errorf("%w: invalid RPM metadata tag %d", errInvalidPackageInput, tag)
		}
		value, _, terminated := bytes.Cut(store[offset:], []byte{0})
		if !terminated || string(value) != want {
			return fmt.Errorf("%w: unexpected RPM metadata tag %d", errInvalidPackageInput, tag)
		}
		seen[tag] = true
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("%w: required RPM metadata is missing", errInvalidPackageInput)
	}
	return nil
}

func readRPMMetadataHeader(reader io.ReaderAt, offset int64) ([]byte, int64, error) {
	var prefix [rpmHeaderSize]byte
	if _, err := reader.ReadAt(prefix[:], offset); err != nil {
		return nil, 0, fmt.Errorf("%w: read RPM header: %w", errInvalidPackageInput, err)
	}
	if string(prefix[:8]) != "\x8e\xad\xe8\x01\x00\x00\x00\x00" {
		return nil, 0, fmt.Errorf("%w: invalid RPM header magic", errInvalidPackageInput)
	}
	entries, storeSize := binary.BigEndian.Uint32(prefix[8:12]), binary.BigEndian.Uint32(prefix[12:16])
	if entries == 0 || entries > maxRPMHeaderEntries || storeSize == 0 || storeSize > maxRPMHeaderStore {
		return nil, 0, fmt.Errorf("%w: RPM header exceeds metadata bounds", errInvalidPackageInput)
	}
	header := make([]byte, rpmHeaderSize+int(entries)*rpmIndexSize+int(storeSize))
	copy(header, prefix[:])
	if _, err := reader.ReadAt(header[rpmHeaderSize:], offset+rpmHeaderSize); err != nil {
		return nil, 0, fmt.Errorf("%w: read RPM header entries/store: %w", errInvalidPackageInput, err)
	}
	return header, offset + int64(len(header)), nil
}

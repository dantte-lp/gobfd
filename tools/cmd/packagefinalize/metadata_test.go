package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestInspectRPMMetadataAcceptsWrittenPackages(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"0.6.5", "1.0.0-alpha.1"} {
		for _, arch := range []string{amd64, arm64} {
			t.Run(version+"/"+arch, func(t *testing.T) {
				t.Parallel()
				data := rpmMetadataFixture(t, version, arch)
				if err := inspectRPMMetadata(bytes.NewReader(data), version, arch); err != nil {
					t.Fatal(err)
				}
				for _, tag := range []uint32{1086, 1087} {
					entry := rpmMetadataTag(t, data, tag)
					binary.BigEndian.PutUint32(entry[4:8], 8)
				}
				if err := inspectRPMMetadata(bytes.NewReader(data), version, arch); err != nil {
					t.Fatalf("compatible single-element program arrays rejected: %v", err)
				}
			})
		}
	}
}

func TestInspectRPMMetadataRejectsCorruption(t *testing.T) {
	t.Parallel()
	fixture := rpmMetadataFixture(t, "1.0.0-alpha.1", amd64)
	for _, tc := range []struct {
		name        string
		tag         uint32
		field       int
		value       uint32
		replacement string
	}{
		{name: "ordinary_array", tag: 1000, field: 4, value: 8},
		{name: "program_integer", tag: 1086, field: 4, value: 4},
		{name: "zero_count", tag: 1086, field: 12},
		{name: "extra_argument", tag: 1087, field: 12, value: 2},
		{name: "ordinary_count", tag: 1001, field: 12, value: 2},
		{name: "offset", tag: 1000, field: 8, value: ^uint32(0)},
		{name: "missing", tag: 1000, value: 9999},
		{name: "duplicate", tag: 1002, value: 1000},
		{name: "name", tag: 1000, replacement: "wrong"},
		{name: "version", tag: 1001, replacement: "1.0.0-alpha.1"},
		{name: "release", tag: 1002, replacement: "2"},
		{name: "architecture", tag: 1022, replacement: "badabi"},
		{name: "post_body", tag: 1024, replacement: "x"},
		{name: "preun_body", tag: 1025, replacement: "x"},
		{name: "post_shell", tag: 1086, replacement: "/bin/sh"},
		{name: "preun_shell", tag: 1087, replacement: "/bin/sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := bytes.Clone(fixture)
			entry := rpmMetadataTag(t, data, tc.tag)
			if tc.replacement != "" {
				store := rpmMetadataStore(data)
				offset := binary.BigEndian.Uint32(entry[8:12])
				copy(store[offset:], tc.replacement+"\x00")
			} else {
				binary.BigEndian.PutUint32(entry[tc.field:tc.field+4], tc.value)
			}
			if err := inspectRPMMetadata(bytes.NewReader(data), "1.0.0-alpha.1", amd64); !errors.Is(err, errInvalidPackageInput) {
				t.Fatalf("corrupted RPM accepted or error lost: %v", err)
			}
		})
	}
	mainOffset := rpmMetadataHeaderOffset(fixture)
	signatureEnd := 112 + int(binary.BigEndian.Uint32(fixture[104:108]))*16 + int(binary.BigEndian.Uint32(fixture[108:112]))
	storeOffset := mainOffset + 16 + int(binary.BigEndian.Uint32(fixture[mainOffset+8:mainOffset+12]))*16
	storeSize := int(binary.BigEndian.Uint32(fixture[mainOffset+12 : mainOffset+16]))
	for _, size := range []int{0, 95, 96, 111, signatureEnd - 1, mainOffset - 1, mainOffset + 15, storeOffset - 1, storeOffset + storeSize - 1} {
		t.Run("truncated/"+strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			if err := inspectRPMMetadata(bytes.NewReader(fixture[:size]), "1.0.0-alpha.1", amd64); !errors.Is(err, errInvalidPackageInput) {
				t.Fatalf("truncated RPM accepted or error lost: %v", err)
			}
		})
	}
	t.Run("unterminated", func(t *testing.T) {
		t.Parallel()
		data := bytes.Clone(fixture)
		store := rpmMetadataStore(data)
		offset := len(store)
		mainOffset := rpmMetadataHeaderOffset(data)
		storeEnd := mainOffset + 16 + int(binary.BigEndian.Uint32(data[mainOffset+8:mainOffset+12]))*16 + offset
		data = append(data[:storeEnd], append(make([]byte, len("gobfd")), data[storeEnd:]...)...)
		binary.BigEndian.PutUint32(data[mainOffset+12:mainOffset+16], uint32(offset+len("gobfd")))
		entry := rpmMetadataTag(t, data, 1000)
		store = rpmMetadataStore(data)
		copy(store[offset:], "gobfd")
		binary.BigEndian.PutUint32(entry[8:12], uint32(offset))
		if err := inspectRPMMetadata(bytes.NewReader(data), "1.0.0-alpha.1", amd64); !errors.Is(err, errInvalidPackageInput) {
			t.Fatalf("unterminated RPM string accepted: %v", err)
		}
	})
}

func TestInspectRPMMetadataRejectsHeaderBounds(t *testing.T) {
	t.Parallel()
	fixture := rpmMetadataFixture(t, "0.6.5", amd64)
	for _, offset := range []int{96, rpmMetadataHeaderOffset(fixture)} {
		for _, field := range []int{8, 12} {
			data := bytes.Clone(fixture)
			binary.BigEndian.PutUint32(data[offset+field:offset+field+4], ^uint32(0))
			if err := inspectRPMMetadata(bytes.NewReader(data), "0.6.5", amd64); !errors.Is(err, errInvalidPackageInput) {
				t.Fatalf("unbounded header accepted or error lost: %v", err)
			}
		}
		data := bytes.Clone(fixture)
		data[offset] ^= 1
		if err := inspectRPMMetadata(bytes.NewReader(data), "0.6.5", amd64); !errors.Is(err, errInvalidPackageInput) {
			t.Fatalf("wrong header magic accepted: %v", err)
		}
	}
	fixture[0] ^= 1
	if err := inspectRPMMetadata(bytes.NewReader(fixture), "0.6.5", amd64); !errors.Is(err, errInvalidPackageInput) {
		t.Fatalf("wrong lead magic accepted: %v", err)
	}
}

func TestInspectRPMMetadataPreservesReadFailure(t *testing.T) {
	t.Parallel()
	err := inspectRPMMetadata(rpmMetadataReadFailure{}, "0.6.5", amd64)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !errors.Is(err, errInvalidPackageInput) {
		t.Fatalf("RPM reader failure not preserved: %v", err)
	}
}

type rpmMetadataReadFailure struct{}

func (rpmMetadataReadFailure) ReadAt([]byte, int64) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestReplaceRPMPublishesWrittenMetadata(t *testing.T) {
	t.Parallel()
	root := writeRPMFixture(t)
	path := filepath.Join(root, "dist", "package.rpm")
	if err := replaceRPM(path, root, "1.0.0-alpha.1", arm64); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for tag, want := range map[uint32]string{
		1000: "gobfd", 1001: "1.0.0~alpha.1", 1002: "1", 1022: "aarch64",
		1024: "\n", 1025: "\n", 1086: "/usr/libexec/gobfd-postinstall", 1087: "/usr/libexec/gobfd-preremove",
	} {
		assertRPMStringTag(t, data, tag, want)
	}
	entries, err := filepath.Glob(filepath.Join(root, "dist", ".gobfd-rpm-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary RPM retained after publication: %v: %v", entries, err)
	}
}

func rpmMetadataFixture(t *testing.T, version, arch string) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := writeRPM(&output, writeRPMFixture(t), version, arch); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func rpmMetadataHeaderOffset(data []byte) int {
	length := 16 + int(binary.BigEndian.Uint32(data[104:108]))*16 + int(binary.BigEndian.Uint32(data[108:112]))
	return 96 + (length+7)/8*8
}

func rpmMetadataStore(data []byte) []byte {
	header := data[rpmMetadataHeaderOffset(data):]
	count := int(binary.BigEndian.Uint32(header[8:12]))
	return header[16+count*16 : 16+count*16+int(binary.BigEndian.Uint32(header[12:16]))]
}

func rpmMetadataTag(t *testing.T, data []byte, tag uint32) []byte {
	t.Helper()
	header := data[rpmMetadataHeaderOffset(data):]
	count := int(binary.BigEndian.Uint32(header[8:12]))
	for index := range count {
		entry := header[16+index*16 : 16+(index+1)*16]
		if binary.BigEndian.Uint32(entry) == tag {
			return entry
		}
	}
	t.Fatalf("fixture lacks RPM tag %d", tag)
	return nil
}

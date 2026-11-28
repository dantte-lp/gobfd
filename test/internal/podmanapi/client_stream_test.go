package podmanapi

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"testing"
)

func TestDemuxDockerStreamPreservesCompleteBinaryFrames(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		stream []byte
		stdout string
		stderr string
	}{
		{name: "empty stream"},
		{
			name: "binary stdout and stderr",
			stream: []byte{
				1, 0, 0, 0, 0, 0, 0, 3, 0, 0xff, 'o',
				2, 0, 0, 0, 0, 0, 0, 3, 'e', 0, 0xfe,
				1, 0, 0, 0, 0, 0, 0, 3, 'k', 0, 0xfd,
			},
			stdout: "\x00\xffok\x00\xfd",
			stderr: "e\x00\xfe",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, err := DemuxDockerStream(bytes.NewReader(test.stream))
			if err != nil {
				t.Fatalf("complete stream error = %v, want nil", err)
			}
			if stdout != test.stdout || stderr != test.stderr {
				t.Fatalf("binary output = (%q, %q), want (%q, %q)", stdout, stderr, test.stdout, test.stderr)
			}
		})
	}
}

func TestDemuxDockerStreamRejectsPartialHeader(t *testing.T) {
	t.Parallel()
	header := []byte{2, 0, 0, 0, 0, 0, 0, 3}
	for _, test := range []struct {
		name   string
		prefix []byte
		stdout string
	}{
		{name: "at stream start"},
		{
			name:   "after binary stdout frame",
			prefix: []byte{1, 0, 0, 0, 0, 0, 0, 3, 0, 0xff, 'a'},
			stdout: "\x00\xffa",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for length := 1; length <= 7; length++ {
				t.Run(strconv.Itoa(length), func(t *testing.T) {
					t.Parallel()
					stream := append([]byte(nil), test.prefix...)
					stream = append(stream, header[:length]...)
					stdout, stderr, err := DemuxDockerStream(bytes.NewReader(stream))
					if !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("partial %d-byte header error = %v, want io.ErrUnexpectedEOF", length, err)
					}
					if stdout != test.stdout || stderr != "" {
						t.Fatalf("partial-header output = (%q, %q), want (%q, empty)", stdout, stderr, test.stdout)
					}
				})
			}
		})
	}
}

func TestDemuxDockerStreamRejectsPartialBinaryBody(t *testing.T) {
	t.Parallel()
	stream := []byte{
		1, 0, 0, 0, 0, 0, 0, 3, 0, 0xff, 'a',
		2, 0, 0, 0, 0, 0, 0, 4, 0, 0xfe,
	}
	stdout, stderr, err := DemuxDockerStream(bytes.NewReader(stream))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial binary body error = %v, want io.ErrUnexpectedEOF", err)
	}
	if stdout != "\x00\xffa" || stderr != "" {
		t.Fatalf("partial-body output = (%q, %q), want (binary stdout, empty)", stdout, stderr)
	}
}

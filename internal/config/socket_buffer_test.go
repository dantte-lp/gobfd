package config_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestSocketBufferValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"read_buffer_size", "write_buffer_size"} {
		for index, value := range []int{-123456789, 0, 8192} {
			t.Run(fmt.Sprintf("%s/case%d", field, index), func(t *testing.T) {
				t.Parallel()
				path := writeTemp(t, fmt.Sprintf("socket:\n  %s: %d\n", field, value))
				cfg, err := config.Load(path)
				if value < 0 {
					want := config.ErrInvalidSocketReadBufferSize
					if field == "write_buffer_size" {
						want = config.ErrInvalidSocketWriteBufferSize
					}
					if !errors.Is(err, want) {
						t.Fatalf("error = %v, want %v", err, want)
					}
					if !strings.Contains(err.Error(), "socket."+field) || strings.Contains(err.Error(), "123456789") {
						t.Fatalf("error must identify bounded field only: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				got := cfg.Socket.ReadBufferSize
				if field == "write_buffer_size" {
					got = cfg.Socket.WriteBufferSize
				}
				if got != value {
					t.Fatalf("buffer = %d, want %d", got, value)
				}
			})
		}
	}
}

func TestSocketBufferRawScalarValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"read_buffer_size", "write_buffer_size"} {
		for index, scalar := range []string{
			"-0.5", "0.5", "true", "9223372036854775808", "18446744073709551616",
			"9223372036854775808.0", ".inf", ".nan",
			`"-0.5"`, `"0.5"`, `"true"`, `"9223372036854775808"`, `"-1"`,
		} {
			t.Run(fmt.Sprintf("%s/case%d", field, index), func(t *testing.T) {
				t.Parallel()
				path := writeTemp(t, fmt.Sprintf("socket:\n  %s: %s\n", field, scalar))
				_, err := config.Load(path)
				want := config.ErrInvalidSocketReadBufferSize
				if field == "write_buffer_size" {
					want = config.ErrInvalidSocketWriteBufferSize
				}
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want typed field error %v", err, want)
				}
			})
		}
	}
}

func TestSocketBufferScalarCompatibility(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"read_buffer_size", "write_buffer_size"} {
		for index, tt := range []struct {
			scalar string
			want   int
		}{
			{"", config.DefaultConfig().Socket.ReadBufferSize},
			{"null", 0},
			{`"0"`, 0},
			{`"8192"`, 8192},
			{`"0x2000"`, 8192},
			{"8192.0", 8192},
		} {
			t.Run(fmt.Sprintf("%s/case%d", field, index), func(t *testing.T) {
				t.Parallel()
				contents := "{}\n"
				if tt.scalar != "" {
					contents = fmt.Sprintf("socket:\n  %s: %s\n", field, tt.scalar)
				}
				cfg, err := config.Load(writeTemp(t, contents))
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				got := cfg.Socket.ReadBufferSize
				if field == "write_buffer_size" {
					got = cfg.Socket.WriteBufferSize
				}
				if got != tt.want {
					t.Fatalf("buffer = %d, want %d", got, tt.want)
				}
			})
		}
	}
}

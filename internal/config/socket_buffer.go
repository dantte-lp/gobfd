package config

import (
	"math"
	"strconv"

	"github.com/knadh/koanf/v2"
)

func validateSocket(cfg SocketConfig) error {
	if cfg.ReadBufferSize < 0 {
		return ErrInvalidSocketReadBufferSize
	}
	if cfg.WriteBufferSize < 0 {
		return ErrInvalidSocketWriteBufferSize
	}
	if cfg.IPv4DSCP < 0 || cfg.IPv4DSCP > maxSocketDSCP {
		return ErrInvalidSocketIPv4DSCP
	}
	if cfg.IPv6TrafficClass < 0 || cfg.IPv6TrafficClass > math.MaxUint8 {
		return ErrInvalidSocketIPv6TrafficClass
	}
	if cfg.Priority < 0 || cfg.Priority > math.MaxInt32 {
		return ErrInvalidSocketPriority
	}
	return nil
}

// Reject truncation, overflow and bool conversion before weak integer decoding.
func validateSocketBufferInput(k *koanf.Koanf) error {
	for _, field := range []struct {
		path string
		err  error
	}{
		{"socket.read_buffer_size", ErrInvalidSocketReadBufferSize},
		{"socket.write_buffer_size", ErrInvalidSocketWriteBufferSize},
	} {
		value, found, err := rawCaseFoldedValue(k.Raw(), field.path)
		if err != nil {
			return err
		}
		if found && !validSocketBufferScalar(value) {
			return field.err
		}
	}
	return nil
}

func validSocketBufferScalar(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		parsed, err := strconv.ParseInt(value, 0, strconv.IntSize)
		return err == nil && parsed >= 0
	case int:
		return value >= 0
	case int64:
		return value >= 0 && value <= math.MaxInt
	case uint64:
		return value <= math.MaxInt
	case float64:
		// The strict bound excludes 2^(IntSize-1), which rounds MaxInt up on 64-bit systems.
		return value >= 0 && value < math.Exp2(float64(strconv.IntSize-1)) && math.Trunc(value) == value
	default:
		return false
	}
}

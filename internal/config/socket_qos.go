package config

import (
	"math"
	"strconv"

	"github.com/knadh/koanf/v2"
)

const maxSocketDSCP = 63

func validateSocketQoSInput(k *koanf.Koanf) error {
	for _, field := range []struct {
		path string
		max  uint64
		err  error
	}{
		{"socket.ipv4_dscp", maxSocketDSCP, ErrInvalidSocketIPv4DSCP},
		{"socket.ipv6_traffic_class", math.MaxUint8, ErrInvalidSocketIPv6TrafficClass},
		{"socket.priority", math.MaxInt32, ErrInvalidSocketPriority},
		{"socket.mark", math.MaxUint32, ErrInvalidSocketMark},
	} {
		value, found, err := rawCaseFoldedValue(k.Raw(), field.path)
		if err != nil {
			return err
		}
		if !found || !validSocketQoSScalar(value, field.max) {
			return field.err
		}
	}
	return validateSocketQoSFailurePolicy(k)
}

func validSocketQoSScalar(value any, maximum uint64) bool {
	switch value := value.(type) {
	case string:
		parsed, err := strconv.ParseUint(value, 0, 32)
		return err == nil && parsed <= maximum
	case int:
		return value >= 0 && uint64(value) <= maximum
	case int64:
		return value >= 0 && uint64(value) <= maximum
	case uint32:
		return uint64(value) <= maximum
	case uint64:
		return value <= maximum
	case float64:
		return value >= 0 && value <= float64(maximum) && math.Trunc(value) == value
	default:
		return false
	}
}

func validateSocketQoSFailurePolicy(k *koanf.Koanf) error {
	for _, field := range []struct {
		path string
		err  error
	}{
		{"socket.priority_required", ErrInvalidSocketPriorityRequired},
		{"socket.mark_required", ErrInvalidSocketMarkRequired},
	} {
		value, found, err := rawCaseFoldedValue(k.Raw(), field.path)
		if err != nil {
			return err
		}
		if !found {
			return field.err
		}
		if _, ok := value.(bool); ok {
			continue
		}
		if text, ok := value.(string); ok {
			if _, parseErr := strconv.ParseBool(text); parseErr == nil {
				continue
			}
		}
		return field.err
	}
	return nil
}

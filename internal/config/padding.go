package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/knadh/koanf/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func validatePadding(cfg *Config) error {
	if err := bfd.ValidatePaddedPduSize(cfg.BFD.DefaultPaddedPduSize, false); err != nil {
		return fmt.Errorf("bfd.default_padded_pdu_size: %w", err)
	}
	for i, session := range cfg.Sessions {
		size := session.PaddedPduSize
		if size == 0 {
			size = cfg.BFD.DefaultPaddedPduSize
		}
		authType := strings.TrimSpace(session.Auth.Type)
		if err := bfd.ValidatePaddedPduSize(size, authType != "" && authType != "none"); err != nil {
			return fmt.Errorf("sessions[%d].padded_pdu_size: %w", i, err)
		}
	}
	return nil
}

// Reject wraparound, fractions and bool conversion before weak uint16 decoding.
func validatePaddingInput(k *koanf.Koanf) error {
	root := k.Raw()
	value, found, err := rawCaseFoldedValue(root, "bfd.default_padded_pdu_size")
	if err != nil {
		return err
	}
	if found && !validPaddingScalar(value) {
		return fmt.Errorf("bfd.default_padded_pdu_size: %w", bfd.ErrInvalidPaddedPduSize)
	}
	value, found, err = rawCaseFoldedValue(root, "sessions")
	if err != nil {
		return err
	}
	if !found || value == nil {
		return nil
	}
	entries, ok := value.([]any)
	if !ok {
		return ErrInvalidSessionShape
	}
	for i, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("sessions[%d]: %w", i, ErrInvalidSessionShape)
		}
		size, found, err := rawCaseFoldedValue(entry, "padded_pdu_size")
		if err != nil {
			return fmt.Errorf("sessions[%d]: %w", i, err)
		}
		if found && !validPaddingScalar(size) {
			return fmt.Errorf("sessions[%d].padded_pdu_size: %w", i, bfd.ErrInvalidPaddedPduSize)
		}
	}
	return nil
}

func validPaddingScalar(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		parsed, err := strconv.ParseUint(value, 0, 16)
		return err == nil && parsed <= bfd.MaxPaddedPduSize
	case int:
		return value >= 0 && value <= bfd.MaxPaddedPduSize
	case int64:
		return value >= 0 && value <= bfd.MaxPaddedPduSize
	case uint64:
		return value <= bfd.MaxPaddedPduSize
	case float64:
		return value >= 0 && value <= bfd.MaxPaddedPduSize && math.Trunc(value) == value
	default:
		return false
	}
}

package config

import (
	"errors"
	"strings"
)

var (
	// ErrInvalidLogLevel rejects unsupported log levels without exposing their values.
	ErrInvalidLogLevel = errors.New("log.level must be debug, info, warn, or error (case-insensitive)")
	// ErrInvalidLogFormat rejects unsupported log formats without exposing their values.
	ErrInvalidLogFormat = errors.New("log.format must be json or text (lowercase)")
)

func validateLog(cfg LogConfig) error {
	switch strings.ToLower(cfg.Level) {
	case "debug", "info", "warn", "error":
	default:
		return ErrInvalidLogLevel
	}
	switch cfg.Format {
	case "json", "text":
		return nil
	default:
		return ErrInvalidLogFormat
	}
}

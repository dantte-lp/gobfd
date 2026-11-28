package config_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestLogVocabularyValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"level", "format"} {
		allowed := []string{"debug", "info", "warn", "error", "DEBUG", "WaRn"}
		invalid := []string{"", " ", " debug", "info ", "trace", "warning", strings.Repeat("private-marker", 512)}
		if field == "format" {
			allowed = []string{"json", "text"}
			invalid = []string{"", " ", "JSON", "Text", " json", "text ", strings.Repeat("private-marker", 512)}
		}
		for _, valid := range []bool{true, false} {
			values := invalid
			if valid {
				values = allowed
			}
			for index, value := range values {
				t.Run(fmt.Sprintf("%s/%t/case%d", field, valid, index), func(t *testing.T) {
					t.Parallel()
					cfg := config.DefaultConfig()
					if field == "level" {
						cfg.Log.Level = value
					} else {
						cfg.Log.Format = value
					}
					err := config.Validate(cfg)
					if valid {
						if err != nil {
							t.Fatalf("valid log config: %v", err)
						}
					} else {
						want := config.ErrInvalidLogLevel
						if field == "format" {
							want = config.ErrInvalidLogFormat
						}
						if !errors.Is(err, want) {
							t.Fatalf("error = %v, want %v", err, want)
						}
						if !strings.Contains(err.Error(), "log."+field) || strings.Contains(err.Error(), "private-marker") {
							t.Fatalf("error must identify bounded field only: %v", err)
						}
					}
				})
			}
		}
	}
}

func TestLogVocabularyLoadDefaultsAndEnvironment(t *testing.T) {
	path := writeTemp(t, "{}\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Fatalf("defaults = %+v", cfg.Log)
	}
	for _, tt := range []struct {
		env, value string
		valid      bool
	}{
		{"GOBFD_LOG_LEVEL", "ERROR", true},
		{"GOBFD_LOG_FORMAT", "text", true},
		{"GOBFD_LOG_LEVEL", "", false},
		{"GOBFD_LOG_LEVEL", "trace", false},
		{"GOBFD_LOG_FORMAT", "", false},
		{"GOBFD_LOG_FORMAT", "JSON", false},
	} {
		t.Run(tt.env+"/"+tt.value, func(t *testing.T) {
			t.Setenv(tt.env, tt.value)
			_, err := config.Load(path)
			if tt.valid {
				if err != nil {
					t.Fatalf("Load valid env: %v", err)
				}
			} else {
				want := config.ErrInvalidLogLevel
				if tt.env == "GOBFD_LOG_FORMAT" {
					want = config.ErrInvalidLogFormat
				}
				if !errors.Is(err, want) {
					t.Fatalf("Load error=%v, want %v", err, want)
				}
			}
		})
	}
}

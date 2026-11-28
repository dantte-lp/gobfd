package main

import (
	"errors"
	"flag"
	"os"
	"testing"
)

func TestImagePackagesRejectInvalidArguments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		want      error
	}{
		{"missing profile", []string{"image-packages"}, flag.ErrHelp},
		{"unknown profile", []string{"image-packages", "unknown"}, os.ErrInvalid},
		{"extra argument", []string{"image-packages", "debug", "extra"}, flag.ErrHelp},
		{"extra development argument", []string{"dev-tools", "extra"}, flag.ErrHelp},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := run(t.Context(), test.arguments); !errors.Is(err, test.want) {
				t.Fatalf("invalid arguments returned %v, want %v", err, test.want)
			}
		})
	}
}

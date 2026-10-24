//go:build linux

package bfdload

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestProfileRejectsReducedQualification(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"reference", "engineering"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			profile := Profile{
				Kind: kind, Sessions: 1000, Interval: 100 * time.Millisecond, DetectMultiplier: 3,
				Duration: 24 * time.Hour, LossPermille: 1, CPULoadPercent: 80,
				BlackholeEvery: 30 * time.Minute, BlackholeFor: time.Second, Seed: 17,
			}
			if kind == "engineering" {
				profile.Sessions, profile.Interval, profile.Duration = 5000, 300*time.Millisecond, 30*time.Minute
			}
			if err := profile.Validate(); err != nil {
				t.Fatal(err)
			}
			profile.GCPressure = true
			profile.SchedulerPressure = true
			profile.SessionChurn = true
			if err := profile.Validate(); err != nil {
				t.Fatalf("additive GC variant rejected: %v", err)
			}
			for _, tc := range []struct {
				name string
				edit func(*Profile)
			}{
				{"sessions", func(p *Profile) { p.Sessions-- }},
				{"duration", func(p *Profile) { p.Duration -= time.Second }},
				{"interval", func(p *Profile) { p.Interval += time.Microsecond }},
				{"multiplier", func(p *Profile) { p.DetectMultiplier = 4 }},
				{"loss", func(p *Profile) { p.LossPermille = 0 }},
				{"blackhole_period", func(p *Profile) { p.BlackholeEvery, p.BlackholeFor = 0, 0 }},
				{"blackhole_width", func(p *Profile) { p.BlackholeFor = time.Millisecond }},
				{"cpu_load", func(p *Profile) { p.CPULoadPercent = 0 }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					changed := profile
					tc.edit(&changed)
					if err := changed.Validate(); !errors.Is(err, ErrProfile) {
						t.Fatalf("reduced qualification profile accepted: %v", err)
					}
				})
			}
		})
	}
}

func TestProfileRejectsInvalidWireAndSchedule(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		edit func(*Profile)
	}{
		{"unknown_kind", func(p *Profile) { p.Kind = "tiny-reference" }},
		{"zero_sessions", func(p *Profile) { p.Sessions = 0 }},
		{"source_port_capacity", func(p *Profile) { p.Sessions = 16385 }},
		{"zero_duration", func(p *Profile) { p.Duration = 0 }},
		{"negative_duration", func(p *Profile) { p.Duration = -1 }},
		{"submicrosecond", func(p *Profile) { p.Interval = time.Nanosecond }},
		{"wire_overflow", func(p *Profile) { p.Interval = (time.Duration(math.MaxUint32) + 1) * time.Microsecond }},
		{"zero_multiplier", func(p *Profile) { p.DetectMultiplier = 0 }},
		{"loss_overflow", func(p *Profile) { p.LossPermille = 1001 }},
		{"cpu_overflow", func(p *Profile) { p.CPULoadPercent = 101 }},
		{"churn_short_window", func(p *Profile) { p.SessionChurn, p.Duration = true, 5*time.Second }},
		{"negative_period", func(p *Profile) { p.BlackholeEvery = -1 }},
		{"negative_width", func(p *Profile) { p.BlackholeFor = -1 }},
		{"width_without_period", func(p *Profile) { p.BlackholeFor = time.Second }},
		{"period_without_width", func(p *Profile) { p.BlackholeEvery = time.Second }},
		{"continuous_blackhole", func(p *Profile) { p.BlackholeEvery, p.BlackholeFor = time.Second, time.Second }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			profile, err := DefaultProfile("fixture")
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(&profile)
			if err := profile.Validate(); !errors.Is(err, ErrProfile) {
				t.Fatalf("invalid profile accepted: %v", err)
			}
		})
	}
	if _, err := DefaultProfile("unknown"); !errors.Is(err, ErrProfile) {
		t.Fatalf("unknown profile silently selected a default: %v", err)
	}
}

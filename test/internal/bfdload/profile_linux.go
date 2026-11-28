//go:build linux

package bfdload

import (
	"errors"
	"fmt"
	"time"
)

const (
	fixtureProfile             = "fixture"
	referenceDuration          = 24 * time.Hour
	engineeringInterval        = 300 * time.Millisecond
	engineeringDuration        = 30 * time.Minute
	blackholePeriod            = 30 * time.Minute
	engineeringBlackholePeriod = 10 * time.Minute
)

// ErrProfile means the requested workload is invalid or reduces a named qualification profile.
var ErrProfile = errors.New("invalid workload profile")

// Profile is the intended workload, not evidence that the runtime admitted or completed it.
type Profile struct {
	Kind              string        `json:"kind"`
	Echo              bool          `json:"echo"`
	Sessions          int           `json:"sessions"`
	Interval          time.Duration `json:"interval_ns"`
	DetectMultiplier  uint8         `json:"detect_multiplier"`
	Duration          time.Duration `json:"duration_ns"`
	LossPermille      uint64        `json:"loss_permille"`
	BlackholeEvery    time.Duration `json:"blackhole_every_ns"`
	BlackholeFor      time.Duration `json:"blackhole_for_ns"`
	CPULoadPercent    uint64        `json:"cpu_load_percent"`
	GCPressure        bool          `json:"gc_pressure"`
	SchedulerPressure bool          `json:"scheduler_pressure"`
	SessionChurn      bool          `json:"session_churn"`
	TXOnlyBlackhole   bool          `json:"tx_only_blackhole"`
	Seed              uint64        `json:"seed"`
}

// DefaultProfile supplies the approved workloads and an explicitly unqualified fixture.
func DefaultProfile(kind string) (Profile, error) {
	profile := Profile{
		Kind: kind, Sessions: 2, Interval: 100 * time.Millisecond, DetectMultiplier: 3,
		Duration: 3 * time.Second, Seed: 1,
	}
	switch kind {
	case fixtureProfile:
		return profile, nil
	case "reference":
		profile.Sessions, profile.Duration = 1000, referenceDuration
	case "engineering":
		profile.Sessions, profile.Interval, profile.Duration = 5000, engineeringInterval, engineeringDuration
	default:
		return Profile{}, fmt.Errorf("workload profile kind %q: %w", kind, ErrProfile)
	}
	profile.LossPermille, profile.CPULoadPercent = 1, 80
	profile.BlackholeEvery, profile.BlackholeFor = blackholePeriod, time.Second
	if kind == "engineering" {
		profile.BlackholeEvery = engineeringBlackholePeriod
	}
	return profile, nil
}

// Validate rejects changes that silently shrink or alter the named qualification inputs.
func (p Profile) Validate() error {
	if err := p.validateDimensions(); err != nil {
		return err
	}
	expected, err := DefaultProfile(p.Kind)
	if err != nil {
		return err
	}
	if p.Kind == fixtureProfile {
		return nil
	}
	expected.Seed = p.Seed
	expected.GCPressure = p.GCPressure
	expected.SchedulerPressure = p.SchedulerPressure
	expected.SessionChurn = p.SessionChurn
	expected.Echo = p.Echo
	if p != expected {
		return fmt.Errorf("workload differs from %s qualification inputs: %w", p.Kind, ErrProfile)
	}
	return nil
}

func (p Profile) validateDimensions() error {
	if p.SessionChurn && p.Duration < sessionChurnMinimum {
		return fmt.Errorf("session churn requires at least six seconds: %w", ErrProfile)
	}
	if p.Sessions < 1 || p.Sessions > sourcePortCapacity || !ownerIntervalFitsWire(p.Interval) ||
		p.DetectMultiplier == 0 || p.Duration <= 0 || p.LossPermille > 1000 || p.CPULoadPercent > 100 {
		return fmt.Errorf("workload count, wire interval, duration, loss, or CPU target: %w", ErrProfile)
	}
	if p.BlackholeEvery < 0 || p.BlackholeFor < 0 ||
		(p.BlackholeEvery == 0) != (p.BlackholeFor == 0) ||
		(p.BlackholeEvery != 0 && p.BlackholeFor >= p.BlackholeEvery) {
		return fmt.Errorf("workload blackhole period and duration: %w", ErrProfile)
	}
	return nil
}

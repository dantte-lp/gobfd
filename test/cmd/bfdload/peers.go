//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func runWorkload(ctx context.Context, args []string, output io.Writer) error {
	cfg, err := parseWorkloadConfig(args[1:])
	if err != nil {
		return err
	}
	var report any
	var runErr error
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	if args[0] == "peers" {
		report, runErr = bfdload.RunPeers(runCtx, cfg, slog.Default())
	} else {
		report, runErr = bfdload.RunOwner(runCtx, cfg, slog.Default())
	}
	// Restore signal termination before stdout can block during JSON encoding.
	stop()
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return errors.Join(runErr, fmt.Errorf("encode UDP workload report: %w", err))
	}
	if runErr != nil {
		return fmt.Errorf("run UDP workload: %w", runErr)
	}
	return nil
}

func parseWorkloadConfig(args []string) (bfdload.PeerConfig, error) {
	var cfg bfdload.PeerConfig
	if len(args) == 0 {
		return cfg, fmt.Errorf("missing workload profile: %w", bfdload.ErrProfile)
	}
	profile, err := bfdload.DefaultProfile(args[0])
	if err != nil {
		return cfg, fmt.Errorf("select workload profile: %w", err)
	}
	cfg.Profile = profile
	flags := flag.NewFlagSet("workload "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	registerProfileFlags(flags, &cfg.Profile)
	multiplier := flags.Uint("detect-multiplier", uint(profile.DetectMultiplier), "BFD detection multiplier")
	sourcePort := flags.Uint("source-port-base", 49152, "first stable peer source port")
	iface := flags.String("interface", "", "explicit interface for all bindings")
	flags.StringVar(&cfg.ControlDirectory, "control-dir", "", "shared private phase-control directory")
	flags.Func("session", "repeat localAddrPort,peerAddrPort for each session", func(value string) error {
		endpoint, err := parseEndpoint(value)
		if err != nil {
			return err
		}
		cfg.Sessions = append(cfg.Sessions, endpoint)
		return nil
	})
	flags.VisitAll(func(item *flag.Flag) {
		if item.Name != "session" {
			item.Value = &singleFlag{Value: item.Value}
		}
	})
	if err := flags.Parse(args[1:]); err != nil {
		return cfg, fmt.Errorf("parse workload flags: %w: %w", bfdload.ErrProfile, err)
	}
	if flags.NArg() != 0 || *multiplier > math.MaxUint8 || *sourcePort > math.MaxUint16 {
		return cfg, fmt.Errorf("unexpected arguments or wire integer overflow: %w", bfdload.ErrProfile)
	}
	cfg.Profile.DetectMultiplier, cfg.SourcePortBase = uint8(*multiplier), uint16(*sourcePort)
	for i := range cfg.Sessions {
		cfg.Sessions[i].Interface = *iface
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("validate workload topology: %w", err)
	}
	return cfg, nil
}

func registerProfileFlags(flags *flag.FlagSet, p *bfdload.Profile) {
	flags.BoolVar(&p.Echo, "echo", p.Echo, "select Echo owners and actual packet returns instead of Control peers")
	flags.IntVar(&p.Sessions, "sessions", p.Sessions, "exact session count")
	flags.DurationVar(&p.Interval, "interval", p.Interval, "desired TX and required RX interval")
	flags.DurationVar(&p.Duration, "duration", p.Duration, "traffic duration")
	flags.Uint64Var(&p.CPULoadPercent, "cpu-load-percent", p.CPULoadPercent,
		"requested owner CPU duty; measured separately")
	flags.BoolVar(&p.GCPressure, "gc-pressure", p.GCPressure, "generate bounded owner allocations without forcing GC")
	flags.BoolVar(&p.SchedulerPressure, "scheduler-pressure", p.SchedulerPressure,
		"generate bounded owner compute/yield contention")
	flags.BoolVar(&p.SessionChurn, "session-churn", p.SessionChurn, "release and recover admitted owner sessions")
	flags.BoolVar(&p.TXOnlyBlackhole, "tx-only-blackhole", p.TXOnlyBlackhole,
		"fixture only: omit peer TX while retaining owner-to-peer input")
	flags.Uint64Var(&p.LossPermille, "loss-permille", p.LossPermille, "intentional loss per thousand in each direction")
	flags.DurationVar(&p.BlackholeEvery, "blackhole-every", p.BlackholeEvery, "blackhole period, zero disables")
	flags.DurationVar(&p.BlackholeFor, "blackhole-for", p.BlackholeFor, "blackhole duration, zero disables")
	flags.Uint64Var(&p.Seed, "seed", p.Seed, "reproducible loss seed")
}

func parseEndpoint(value string) (bfdload.OwnerEndpoint, error) {
	var endpoint bfdload.OwnerEndpoint
	local, peer, found := strings.Cut(value, ",")
	if !found {
		return endpoint, fmt.Errorf("expected localAddrPort,peerAddrPort: %w", bfdload.ErrProfile)
	}
	var err error
	endpoint.Local, err = netip.ParseAddrPort(local)
	if err != nil {
		return endpoint, fmt.Errorf("parse local endpoint: %w: %w", bfdload.ErrProfile, err)
	}
	endpoint.Peer, err = netip.ParseAddrPort(peer)
	if err != nil {
		return endpoint, fmt.Errorf("parse peer endpoint: %w: %w", bfdload.ErrProfile, err)
	}
	return endpoint, nil
}

type singleFlag struct {
	flag.Value

	seen bool
}

func (v *singleFlag) String() string {
	if v == nil || v.Value == nil {
		return ""
	}
	return v.Value.String()
}

func (v *singleFlag) IsBoolFlag() bool {
	value, ok := v.Value.(interface{ IsBoolFlag() bool })
	return ok && value.IsBoolFlag()
}

func (v *singleFlag) Set(value string) error {
	if v.seen {
		return fmt.Errorf("duplicate scalar flag: %w", bfdload.ErrProfile)
	}
	v.seen = true
	if err := v.Value.Set(value); err != nil {
		return fmt.Errorf("set scalar flag: %w", err)
	}
	return nil
}

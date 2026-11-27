package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dantte-lp/gobfd/test/internal/repoquality"
)

func runCode(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("code", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	manifest := flags.String("files", "", "optional NUL-delimited git ls-files input")
	format := flags.String("format", "json", "json or mermaid")
	cpu := flags.String("cpu", "", "optional pprof -top -unit=ns text table")
	cpuSource := flags.String("cpu-source", "", "source SHA256 recorded before profiling")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse code inventory flags: %w", err)
	}
	if flags.NArg() != 0 || (*format != "json" && *format != "mermaid") {
		return fmt.Errorf("unexpected code inventory arguments: %w", flag.ErrHelp)
	}
	files, err := codeInputs(ctx, *root, *manifest)
	if err != nil {
		return err
	}
	report, err := repoquality.InspectCode(ctx, *root, files)
	if err != nil {
		return fmt.Errorf("inspect code inventory: %w", err)
	}
	if err := attachCodeProfile(&report, *cpu, *cpuSource); err != nil {
		return err
	}
	if *format == "mermaid" {
		if _, err := io.WriteString(os.Stdout, repoquality.CodeDependencyGraph(report)); err != nil {
			return fmt.Errorf("write dependency graph: %w", err)
		}
		return nil
	}
	if err := repoquality.WriteCodeReport(os.Stdout, report); err != nil {
		return fmt.Errorf("write code inventory: %w", err)
	}
	return nil
}

func codeInputs(ctx context.Context, root, manifest string) ([]string, error) {
	if manifest == "" {
		files, err := repoquality.DiscoverGoSources(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("discover code inventory inputs: %w", err)
		}
		return files, nil
	}
	data, err := readCodeArtifact(manifest)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\x00' {
		return nil, fmt.Errorf("invalid code inventory manifest size or delimiter: %w", os.ErrInvalid)
	}
	return strings.Split(string(data[:len(data)-1]), "\x00"), nil
}

func attachCodeProfile(report *repoquality.CodeReport, cpu, source string) error {
	if cpu == "" && source == "" {
		return nil
	}
	data, err := readCodeArtifact(cpu)
	if err != nil {
		return err
	}
	if err := repoquality.AttachCodeCPU(report, data, source); err != nil {
		return fmt.Errorf("attach CPU evidence: %w", err)
	}
	return nil
}

func readCodeArtifact(name string) ([]byte, error) {
	const maximum = 16 << 20
	info, err := os.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect code audit artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maximum {
		return nil, fmt.Errorf("code audit artifact is nonregular or oversized: %w", os.ErrInvalid)
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open code audit artifact: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	opened, statErr := file.Stat()
	if err := errors.Join(readErr, statErr, file.Close()); err != nil {
		return nil, fmt.Errorf("read code audit artifact: %w", err)
	}
	if !os.SameFile(info, opened) || !info.ModTime().Equal(opened.ModTime()) || int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("code audit artifact changed while reading: %w", os.ErrInvalid)
	}
	return data, nil
}

package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

const receiveFixtureContract = "decode-packet-per-iteration-v1"

type receiveFixtureProvenance struct {
	Contract string            `json:"contract"`
	SHA256   map[string]string `json:"sha256"`
}

type receiveFixtureRunner struct {
	runner  SpecRunner
	overlay string
}

func (r receiveFixtureRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	if spec.Name == "go" && len(spec.Args) != 0 && spec.Args[0] == goTestSubcommand {
		spec.Args = append([]string{goTestSubcommand, "-overlay=" + r.overlay}, spec.Args[1:]...)
	}
	if err := r.runner.RunCommand(ctx, spec); err != nil {
		return fmt.Errorf("run receive fixture command %s: %w", spec.Name, err)
	}
	return nil
}

func runFrozenBenchmarkCollection(
	ctx context.Context, options BenchmarkBaseOptions, worktree, output, headPath, frozenCommit string,
) (result error) {
	if err := verifyReleaseBenchmarkHead(ctx, options.Runner, worktree, frozenCommit); err != nil {
		return err
	}
	overlay, err := prepareReceiveFixtureOverlay(ctx, options, worktree)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, wrapOptional("remove receive fixture overlay", os.Remove(overlay)))
	}()
	options.Runner = receiveFixtureRunner{runner: options.Runner, overlay: overlay}
	if err := checkReleaseBenchmarkRace(ctx, options, worktree); err != nil {
		return err
	}
	if err := runBenchmarkCollection(ctx, options, worktree, output, headPath); err != nil {
		return err
	}
	return verifyReleaseBenchmarkHead(ctx, options.Runner, worktree, frozenCommit)
}

func prepareReceiveFixtureOverlay(ctx context.Context, options BenchmarkBaseOptions, worktree string) (string, error) {
	replacements := make(map[string]string)
	for _, side := range []struct{ name, directory string }{{"baseline", worktree}, {"current", options.Root}} {
		source, replacement, err := stageReceiveFixture(ctx, options, side.directory, side.name)
		if err != nil {
			return "", err
		}
		replacements[source] = replacement
	}
	provenance, err := hashReceiveFixtures(options.Root)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode receive fixture provenance: %w", err)
	}
	manifest, err := validateRootFile(options.Root, "reports/benchmarks/pair-fixture-provenance.json",
		"receive fixture provenance", true)
	if err != nil {
		return "", err
	}
	if writeErr := writeAtomicArtifact(manifest, append(data, '\n'), "receive fixture provenance"); writeErr != nil {
		return "", writeErr
	}
	data, err = json.Marshal(map[string]map[string]string{"Replace": replacements})
	if err != nil {
		return "", fmt.Errorf("encode receive fixture overlay: %w", err)
	}
	file, err := os.CreateTemp(options.RunnerTemp, "gobfd-receive-overlay-*.json")
	if err != nil {
		return "", fmt.Errorf("create receive fixture overlay: %w", err)
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", errors.Join(fmt.Errorf("write receive fixture overlay: %w", err),
			wrapOptional("remove incomplete receive fixture overlay", os.Remove(file.Name())))
	}
	return file.Name(), nil
}

func stageReceiveFixture(
	ctx context.Context, options BenchmarkBaseOptions, directory, side string,
) (string, string, error) {
	const name = "internal/bfd/bench_test.go"
	source, err := validateRootFile(directory, name, "receive benchmark source", false)
	if err != nil {
		return "", "", err
	}
	data, err := readRegularFile(source, "receive benchmark source")
	if err != nil {
		return "", "", err
	}
	blob, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "git", Dir: directory, Args: []string{"show", "HEAD:" + name},
	}, "verify receive benchmark source blob")
	if err != nil {
		return "", "", err
	}
	if !bytes.Equal(data, blob) {
		return "", "", fmt.Errorf("%s receive fixture differs from its committed blob: %w", side, errInvalidConfig)
	}
	corrected, err := ownedReceiveFixture(data)
	if err != nil {
		return "", "", err
	}
	var replacement string
	for index, content := range [][]byte{data, corrected} {
		replacement, err = validateRootFile(options.Root, receiveFixtureArtifact(side, index), "receive fixture", true)
		if err != nil {
			return "", "", err
		}
		if err := writeAtomicArtifact(replacement, content, "receive fixture"); err != nil {
			return "", "", err
		}
	}
	return source, replacement, nil
}

// The historical queue retains a packet pointer; each decode must own its storage.
func ownedReceiveFixture(source []byte) ([]byte, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "bench_test.go", source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse receive fixture: %w", err)
	}
	var body *ast.BlockStmt
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if ok && fn.Recv == nil && (fn.Name.Name == "BenchmarkRecvDecodeLookupEnqueue" ||
			fn.Name.Name == "BenchmarkFullRecvPath") {
			if body != nil {
				return nil, fmt.Errorf("duplicate receive fixture: %w", errInvalidConfig)
			}
			body = fn.Body
		}
	}
	if body == nil {
		return nil, fmt.Errorf("receive fixture is absent: %w", errInvalidConfig)
	}
	before := []byte("\tvar pkt bfd.ControlPacket\n\n\tb.ResetTimer()\n\tb.ReportAllocs()\n\tfor b.Loop() {\n")
	start, end := int(body.Pos())-1, int(body.End())-1
	if bytes.Count(source[start:end], before) != 1 {
		return nil, fmt.Errorf("unrecognized receive fixture ownership: %w", errInvalidConfig)
	}
	after := []byte("\tb.ResetTimer()\n\tb.ReportAllocs()\n\tfor b.Loop() {\n\t\tvar pkt bfd.ControlPacket\n")
	result := append([]byte(nil), source[:start]...)
	result = append(result, bytes.Replace(source[start:end], before, after, 1)...)
	return append(result, source[end:]...), nil
}

func receiveFixtureArtifact(side string, index int) string {
	suffix := "original"
	if index == 1 {
		suffix = "owned"
	}
	return "reports/benchmarks/pair-" + side + "-fixture-" + suffix + ".go.txt"
}

func hashReceiveFixtures(root string) (receiveFixtureProvenance, error) {
	provenance := receiveFixtureProvenance{Contract: receiveFixtureContract, SHA256: make(map[string]string)}
	for _, side := range []string{"baseline", "current"} {
		for index := range 2 {
			name := receiveFixtureArtifact(side, index)
			path, err := validateRootFile(root, name, "receive fixture", false)
			if err != nil {
				return provenance, err
			}
			data, err := readRegularFile(path, "receive fixture")
			if err != nil {
				return provenance, err
			}
			digest := sha256.Sum256(data)
			provenance.SHA256[filepath.Base(name)] = hex.EncodeToString(digest[:])
		}
	}
	return provenance, nil
}

func verifyReceiveFixtureEvidence(root string) (receiveFixtureProvenance, error) {
	actual, err := hashReceiveFixtures(root)
	if err != nil {
		return actual, err
	}
	path, err := validateRootFile(root, "reports/benchmarks/pair-fixture-provenance.json",
		"receive fixture provenance", false)
	if err != nil {
		return actual, err
	}
	data, err := readRegularFile(path, "receive fixture provenance")
	if err != nil {
		return actual, err
	}
	expected, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		return actual, fmt.Errorf("encode receive fixture provenance: %w", err)
	}
	if !bytes.Equal(data, append(expected, '\n')) {
		return actual, fmt.Errorf("receive fixture evidence changed during collection: %w", errInvalidConfig)
	}
	return actual, nil
}

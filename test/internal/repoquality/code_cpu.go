package repoquality

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"strconv"
	"strings"
)

var errCodeCPUAmbiguous = errors.New("ambiguous CPU symbol")

// CodeCPUCost is sampled total CPU, not a duration or invocation count.
type CodeCPUCost struct {
	FlatNS       uint64 `json:"flat_ns"`
	CumulativeNS uint64 `json:"cumulative_ns"`
}

// CodeCPUProfile binds an imported pprof text table to the source inventory.
type CodeCPUProfile struct {
	SourceSHA256 string `json:"source_sha256"`
	TableSHA256  string `json:"table_sha256"`
	Matched      int    `json:"matched_declarations"`
}

// AttachCodeCPU imports only pprof -top -unit=ns output for the named source hash.
func AttachCodeCPU(report *CodeReport, data []byte, sourceSHA256 string) error {
	if sourceSHA256 == "" || sourceSHA256 != report.SourceSHA256 {
		return fmt.Errorf("CPU profile source does not match code inventory: %w", fs.ErrInvalid)
	}
	costs, err := parseCodeCPU(data)
	if err != nil {
		return err
	}
	packages := make(map[string]string)
	for _, file := range report.Files {
		packages[file.Path] = file.Package
	}
	matched := make(map[int]CodeCPUCost)
	seen := make(map[string]bool)
	for index, fn := range report.Functions {
		name := codeRuntimeSymbol(fn, packages[fn.Path])
		if cost, found := costs[name]; found {
			if seen[name] {
				return fmt.Errorf("CPU symbol %s maps to multiple source variants: %w", name, errCodeCPUAmbiguous)
			}
			seen[name] = true
			matched[index] = cost
		}
	}
	if len(matched) == 0 {
		return fmt.Errorf("CPU profile has no matching declarations: %w", fs.ErrInvalid)
	}
	for index := range report.Functions {
		report.Functions[index].CPU = nil
		report.Functions[index].Runtime = "not_observed"
		if cost, found := matched[index]; found {
			report.Functions[index].CPU = &cost
			report.Functions[index].Runtime = "sampled_cpu"
		}
	}
	report.CPUProfile = &CodeCPUProfile{SourceSHA256: sourceSHA256, TableSHA256: codeDigest(data), Matched: len(matched)}
	return nil
}

func parseCodeCPU(data []byte) (map[string]CodeCPUCost, error) {
	if !bytes.Contains(append([]byte{'\n'}, data...), []byte("\nType: cpu\n")) {
		return nil, fmt.Errorf("expected CPU pprof table: %w", fs.ErrInvalid)
	}
	rows := make(map[string]CodeCPUCost)
	lexer := bufio.NewScanner(bytes.NewReader(data))
	const maximumRow = 1 << 20
	lexer.Buffer(nil, maximumRow)
	inRows := false
	for lexer.Scan() {
		fields := strings.Fields(lexer.Text())
		if !inRows {
			inRows = strings.Join(fields, " ") == "flat flat% sum% cum cum%"
			continue
		}
		if len(fields) == 0 {
			continue
		}
		name, cost, err := parseCodeCPURow(fields)
		if err != nil {
			return nil, err
		}
		if _, duplicate := rows[name]; duplicate {
			return nil, fmt.Errorf("duplicate CPU symbol %s: %w", name, fs.ErrInvalid)
		}
		rows[name] = cost
	}
	if err := lexer.Err(); err != nil {
		return nil, fmt.Errorf("scan CPU table: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty CPU table: %w", fs.ErrInvalid)
	}
	return rows, nil
}

func parseCodeCPURow(fields []string) (string, CodeCPUCost, error) {
	const minimumFields = 6
	if len(fields) < minimumFields {
		return "", CodeCPUCost{}, fmt.Errorf("incomplete CPU row: %w", fs.ErrInvalid)
	}
	for _, value := range []string{fields[1], fields[2], fields[4]} {
		if err := validateCodeCPUPercentage(value); err != nil {
			return "", CodeCPUCost{}, err
		}
	}
	flat, err := codeCPUNanoseconds(fields[0])
	if err != nil {
		return "", CodeCPUCost{}, err
	}
	cumulative, err := codeCPUNanoseconds(fields[3])
	if err != nil {
		return "", CodeCPUCost{}, err
	}
	if cumulative < flat {
		return "", CodeCPUCost{}, fmt.Errorf("CPU cumulative cost is smaller than flat cost: %w", fs.ErrInvalid)
	}
	name, err := parseCodeCPUSymbol(fields[5:])
	if err != nil {
		return "", CodeCPUCost{}, err
	}
	return name, CodeCPUCost{FlatNS: flat, CumulativeNS: cumulative}, nil
}

func parseCodeCPUSymbol(fields []string) (string, error) {
	name := strings.Join(fields, " ")
	for _, suffix := range []string{" (inline)", " (partial-inline)"} {
		if trimmed, found := strings.CutSuffix(name, suffix); found {
			name = trimmed
			break
		}
	}
	depth := 0
	for _, symbol := range name {
		switch symbol {
		case '[':
			depth++
		case ']':
			depth--
			if depth < 0 {
				return "", fmt.Errorf("unbalanced CPU symbol brackets: %w", fs.ErrInvalid)
			}
		case ' ':
			if depth == 0 {
				return "", fmt.Errorf("unexpected trailing CPU row fields: %w", fs.ErrInvalid)
			}
		}
	}
	if depth != 0 {
		return "", fmt.Errorf("unbalanced CPU symbol brackets: %w", fs.ErrInvalid)
	}
	return name, nil
}

func validateCodeCPUPercentage(value string) error {
	if !strings.HasSuffix(value, "%") {
		return fmt.Errorf("missing CPU percentage unit: %w", fs.ErrInvalid)
	}
	number, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 100 {
		return fmt.Errorf("invalid CPU percentage %q: %w", value, fs.ErrInvalid)
	}
	return nil
}

func codeCPUNanoseconds(value string) (uint64, error) {
	if !strings.HasSuffix(value, "ns") && value != "0" {
		return 0, fmt.Errorf("CPU table unit must be ns: %w", fs.ErrInvalid)
	}
	result, err := strconv.ParseUint(strings.TrimSuffix(value, "ns"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse CPU nanoseconds: %w", err)
	}
	return result, nil
}

func codeRuntimeSymbol(fn CodeFunction, filePackage string) string {
	if fn.Kind != "declaration" {
		return ""
	}
	directory := path.Dir(fn.Path)
	if strings.HasSuffix(filePackage, "_test") {
		directory += "_test"
	}
	receiver := fn.Receiver
	if strings.HasPrefix(receiver, "*") {
		receiver = "(" + receiver + ")"
	}
	if receiver != "" {
		receiver += "."
	}
	return "github.com/dantte-lp/gobfd/" + directory + "." + receiver + fn.Name
}

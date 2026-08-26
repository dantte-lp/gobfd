// Package benchreport validates benchmark outputs and atomically renders the
// repository cross-implementation report.
package benchreport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxInputSize        = 2 << 20
	maximumTempAttempts = 32
	reportFileMode      = 0o600
	reportMarker        = "__BENCHMARK_DATA__"
	metricPairSize      = 2
)

var (
	errInvalidInput  = errors.New("invalid benchmark input")
	errUnsafeInput   = errors.New("unsafe benchmark input")
	errInvalidOutput = errors.New("invalid benchmark report output")
)

// Options identifies all report inputs and the metadata embedded in the output.
type Options struct {
	GoInput    string
	FRRInput   string
	BIRDInput  string
	Metadata   string
	Template   string
	Output     string
	Generated  string
	Platform   string
	GCCVersion string
	GoMaxProcs string
}

type goEntry struct {
	name     string
	nsOp     float64
	bytesOp  int64
	allocsOp int64
}

type goInput struct {
	entries  []goEntry
	platform string
}

type cInput struct {
	averages map[string]float64
	order    []string
}

type metadata struct {
	version string
	goName  string
}

type report struct {
	Meta            reportMetadata   `json:"meta"`
	Implementations []implementation `json:"implementations"`
	Benchmarks      []benchmark      `json:"benchmarks"`
	Features        []feature        `json:"features"`
}

type reportMetadata struct {
	Generated  string `json:"generated"`
	Version    string `json:"gobfd_version"`
	GoVersion  string `json:"go_version"`
	GCCVersion string `json:"gcc_version"`
	Platform   string `json:"platform"`
	GoMaxProcs string `json:"gomaxprocs"`
}

type implementation struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ShortName     string `json:"short_name"`
	Color         string `json:"color"`
	HasAllocs     bool   `json:"has_allocs"`
	HeadlineValue string `json:"headline_value"`
	HeadlineUnit  string `json:"headline_unit"`
	HeadlineLabel string `json:"headline_label"`
}

type benchmark struct {
	Name       string      `json:"name"`
	Go         *goMetric   `json:"go,omitempty"`
	FRR        *cMetric    `json:"frr,omitempty"`
	BIRD       *cMetric    `json:"bird,omitempty"`
	Annotation *annotation `json:"annotation,omitempty"`
}

type goMetric struct {
	NSOp     float64 `json:"ns_op"`
	BytesOp  int64   `json:"b_op"`
	AllocsOp int64   `json:"allocs_op"`
}

type cMetric struct {
	NSOp float64 `json:"ns_op"`
}

type annotation struct {
	GoOnly string `json:"go_only,omitempty"`
	Note   string `json:"note,omitempty"`
}

type feature struct {
	Name string `json:"name"`
	Go   any    `json:"go"`
	FRR  any    `json:"frr"`
	BIRD any    `json:"bird"`
}

// Render validates every input, builds the report, and atomically replaces the output.
func Render(ctx context.Context, options Options) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("render benchmark report: %w", contextErr)
	}
	parsedGo, err := parseGoInput(options.GoInput)
	if err != nil {
		return err
	}
	frr, err := parseCInput(options.FRRInput, "frr")
	if err != nil {
		return err
	}
	bird, err := parseCInput(options.BIRDInput, "bird")
	if err != nil {
		return err
	}
	meta, err := loadMetadata(options.Metadata)
	if err != nil {
		return err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("render benchmark report after parsing inputs: %w", contextErr)
	}
	platform := options.Platform
	if platform == "" {
		platform = parsedGo.platform
	}
	if platform == "" {
		platform = "unknown"
	}
	document, err := buildReport(parsedGo.entries, frr, bird, meta, options, platform)
	if err != nil {
		return err
	}
	if err := writeReport(ctx, options.Template, options.Output, document); err != nil {
		return err
	}
	return nil
}

func parseGoInput(path string) (goInput, error) {
	data, found, err := readRegularNonempty(path, "Go benchmark input", false)
	if err != nil {
		return goInput{}, err
	}
	if !found {
		return goInput{}, invalidInputf("required Go benchmark input does not exist: %s", path)
	}
	var result goInput
	var goos string
	var goarch string
	var cpu string
	for lineIndex, line := range strings.Split(string(data), "\n") {
		lineNumber := lineIndex + 1
		stripped := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(stripped, "goos:"):
			goos = strings.TrimSpace(strings.TrimPrefix(stripped, "goos:"))
			continue
		case strings.HasPrefix(stripped, "goarch:"):
			goarch = strings.TrimSpace(strings.TrimPrefix(stripped, "goarch:"))
			continue
		case strings.HasPrefix(stripped, "cpu:"):
			cpu = strings.TrimSpace(strings.TrimPrefix(stripped, "cpu:"))
			continue
		case stripped == "", stripped == "PASS", strings.HasPrefix(stripped, "pkg:"),
			strings.HasPrefix(stripped, "ok\t"), strings.HasPrefix(stripped, "ok  "):
			continue
		case !strings.HasPrefix(stripped, "Benchmark"):
			return goInput{}, invalidInputf("%s:%d: malformed Go benchmark output", path, lineNumber)
		}
		entry, parseErr := parseGoRecord(path, lineNumber, stripped)
		if parseErr != nil {
			return goInput{}, parseErr
		}
		result.entries = append(result.entries, entry)
	}
	if len(result.entries) == 0 {
		return goInput{}, invalidInputf("%s: no valid Go benchmark records", path)
	}
	parts := make([]string, 0, 2)
	if cpu != "" {
		parts = append(parts, cpu)
	}
	if goos != "" && goarch != "" {
		parts = append(parts, goos+"/"+goarch)
	}
	result.platform = strings.Join(parts, ", ")
	return result, nil
}

func parseGoRecord(path string, lineNumber int, line string) (goEntry, error) {
	fields := strings.Fields(line)
	location := fmt.Sprintf("%s:%d", path, lineNumber)
	if len(fields) < 4 || (len(fields)-metricPairSize)%metricPairSize != 0 {
		return goEntry{}, invalidInputf("%s: malformed Go benchmark record", location)
	}
	if err := positiveInteger(fields[1], location+" iterations"); err != nil {
		return goEntry{}, err
	}
	metrics := make(map[string]float64, (len(fields)-metricPairSize)/metricPairSize)
	for index := 2; index < len(fields); index += 2 {
		unit := fields[index+1]
		if _, duplicate := metrics[unit]; duplicate {
			return goEntry{}, invalidInputf("%s: duplicate metric %q", location, unit)
		}
		value, err := nonNegativeFloat(fields[index], location+" "+unit)
		if err != nil {
			return goEntry{}, err
		}
		metrics[unit] = value
	}
	nsOp, ok := metrics["ns/op"]
	if !ok {
		return goEntry{}, invalidInputf("%s: missing ns/op metric", location)
	}
	name := trimBenchmarkCPU(fields[0])
	name = strings.TrimPrefix(name, "Benchmark")
	if name == "" {
		return goEntry{}, invalidInputf("%s: benchmark name is empty", location)
	}
	entry := goEntry{name: name, nsOp: nsOp}
	if value, present := metrics["B/op"]; present {
		bytesOp, metricErr := allocationMetric(value, location+" B/op")
		if metricErr != nil {
			return goEntry{}, metricErr
		}
		entry.bytesOp = bytesOp
	}
	if value, present := metrics["allocs/op"]; present {
		allocsOp, metricErr := allocationMetric(value, location+" allocs/op")
		if metricErr != nil {
			return goEntry{}, metricErr
		}
		entry.allocsOp = allocsOp
	}
	return entry, nil
}

func trimBenchmarkCPU(name string) string {
	separator := strings.LastIndexByte(name, '-')
	if separator < 0 || separator == len(name)-1 {
		return name
	}
	for _, character := range name[separator+1:] {
		if character < '0' || character > '9' {
			return name
		}
	}
	return name[:separator]
}

func parseCInput(path, implementationName string) (cInput, error) {
	data, found, err := readRegularNonempty(path, implementationName+" benchmark input", false)
	if err != nil {
		return cInput{}, err
	}
	if !found {
		return cInput{}, invalidInputf(
			"required %s benchmark input does not exist: %s",
			implementationName,
			path,
		)
	}
	sums := make(map[string]float64)
	counts := make(map[string]int)
	order := make([]string, 0)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for lineIndex, line := range lines {
		location := fmt.Sprintf("%s:%d", path, lineIndex+1)
		if line == "" {
			return cInput{}, invalidInputf("%s: empty benchmark record", location)
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[0] != "BENCH" {
			return cInput{}, invalidInputf("%s: malformed BENCH record", location)
		}
		if fields[1] != implementationName {
			return cInput{}, invalidInputf(
				"%s: implementation is %q, expected %q",
				location,
				fields[1],
				implementationName,
			)
		}
		if fields[2] == "" {
			return cInput{}, invalidInputf("%s: benchmark name is empty", location)
		}
		duration, parseErr := nonNegativeFloat(fields[3], location+" ns/op")
		if parseErr != nil {
			return cInput{}, parseErr
		}
		if parseErr = positiveInteger(fields[4], location+" iterations"); parseErr != nil {
			return cInput{}, parseErr
		}
		if counts[fields[2]] == 0 {
			order = append(order, fields[2])
		}
		sums[fields[2]] += duration
		counts[fields[2]]++
	}
	if len(counts) == 0 {
		return cInput{}, invalidInputf("%s: no valid %s benchmark records", path, implementationName)
	}
	averages := make(map[string]float64, len(sums))
	for name, sum := range sums {
		averages[name] = roundThree(sum / float64(counts[name]))
	}
	return cInput{averages: averages, order: order}, nil
}

func loadMetadata(path string) (metadata, error) {
	data, found, err := readRegularNonempty(path, "benchmark metadata JSON", true)
	if err != nil {
		return metadata{}, err
	}
	if !found {
		return metadata{version: "unknown", goName: "unknown"}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var values map[string]any
	if decodeErr := decoder.Decode(&values); decodeErr != nil {
		return metadata{}, fmt.Errorf("%w: %s: malformed benchmark metadata JSON: %w", errInvalidInput, path, decodeErr)
	}
	var trailing json.RawMessage
	if trailingErr := decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return metadata{}, invalidInputf("%s: benchmark metadata contains more than one JSON document", path)
		}
		return metadata{}, fmt.Errorf(
			"%w: %s: decode trailing benchmark metadata JSON: %w",
			errInvalidInput,
			path,
			trailingErr,
		)
	}
	if values == nil {
		return metadata{}, invalidInputf("%s: benchmark metadata JSON must be an object", path)
	}
	version, err := optionalMetadataString(path, values, "version")
	if err != nil {
		return metadata{}, err
	}
	goName, err := optionalMetadataString(path, values, "go")
	if err != nil {
		return metadata{}, err
	}
	return metadata{version: version, goName: goName}, nil
}

func optionalMetadataString(path string, values map[string]any, key string) (string, error) {
	value, present := values[key]
	if !present {
		return "unknown", nil
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", invalidInputf("%s: benchmark metadata %q must be a non-empty string", path, key)
	}
	return text, nil
}

func nonNegativeFloat(value, location string) (float64, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid number %q: %w", location, value, err)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, invalidInputf("%s: value must be finite, got %q", location, value)
	}
	if number < 0 {
		return 0, invalidInputf("%s: value must be non-negative, got %q", location, value)
	}
	return number, nil
}

func positiveInteger(value, location string) error {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %s: invalid integer %q: %w", errInvalidInput, location, value, err)
	}
	if number <= 0 {
		return invalidInputf("%s: value must be positive, got %q", location, value)
	}
	return nil
}

func allocationMetric(value float64, location string) (int64, error) {
	if math.Trunc(value) != value || value > math.MaxInt64 {
		return 0, invalidInputf("%s: allocation metric must be an integer, got %g", location, value)
	}
	return int64(value), nil
}

func roundThree(value float64) float64 {
	return math.RoundToEven(value*1000) / 1000
}

func buildReport(
	goEntries []goEntry,
	frrData cInput,
	birdData cInput,
	meta metadata,
	options Options,
	platform string,
) (report, error) {
	goAverage := averageGoEntries(goEntries)
	if _, ok := goAverage["ControlPacketMarshal"]; !ok {
		return report{}, invalidInputf("Go benchmark input is missing required ControlPacketMarshal headline")
	}
	if _, ok := frrData.averages["Marshal"]; !ok {
		return report{}, invalidInputf("FRR benchmark input is missing required FRR Marshal headline")
	}
	if _, ok := birdData.averages["Marshal"]; !ok {
		return report{}, invalidInputf("BIRD benchmark input is missing required BIRD Marshal headline")
	}
	benchmarks := buildBenchmarks(goEntries, goAverage, frrData, birdData)
	goMarshal := goAverage["ControlPacketMarshal"].NSOp
	frrMarshal := frrData.averages["Marshal"]
	birdMarshal := birdData.averages["Marshal"]
	return report{
		Meta: reportMetadata{
			Generated:  options.Generated,
			Version:    meta.version,
			GoVersion:  meta.goName,
			GCCVersion: options.GCCVersion,
			Platform:   platform,
			GoMaxProcs: options.GoMaxProcs,
		},
		Implementations: []implementation{
			{
				ID: "go", Name: "GoBFD (Go)", ShortName: "Go", Color: "#00d4ff", HasAllocs: true,
				HeadlineValue: oneDecimal(goMarshal), HeadlineUnit: "ns/op", HeadlineLabel: "Packet Marshal",
			},
			{
				ID: "frr", Name: "C (FRR bfdd)", ShortName: "C-FRR", Color: "#4ade80",
				HeadlineValue: oneDecimal(frrMarshal), HeadlineUnit: "ns/op", HeadlineLabel: "Marshal (stack alloc)",
			},
			{
				ID: "bird", Name: "C (BIRD BFD)", ShortName: "C-BIRD", Color: "#fbbf24",
				HeadlineValue: oneDecimal(birdMarshal), HeadlineUnit: "ns/op", HeadlineLabel: "Marshal (pre-alloc)",
			},
		},
		Benchmarks: benchmarks,
		Features:   reportFeatures(),
	}, nil
}

func averageGoEntries(entries []goEntry) map[string]goMetric {
	type aggregate struct {
		nsSum    float64
		count    int
		bytesOp  int64
		allocsOp int64
	}
	values := make(map[string]aggregate)
	for _, entry := range entries {
		value := values[entry.name]
		value.nsSum += entry.nsOp
		value.count++
		value.bytesOp = entry.bytesOp
		value.allocsOp = entry.allocsOp
		values[entry.name] = value
	}
	averages := make(map[string]goMetric, len(values))
	for name, value := range values {
		averages[name] = goMetric{
			NSOp:     roundThree(value.nsSum / float64(value.count)),
			BytesOp:  value.bytesOp,
			AllocsOp: value.allocsOp,
		}
	}
	return averages
}

func buildBenchmarks(
	goEntries []goEntry,
	goAverage map[string]goMetric,
	frrData cInput,
	birdData cInput,
) []benchmark {
	canonical := canonicalNames()
	reverse := reverseCanonicalNames(goAverage, canonical)
	ordered := orderedBenchmarkNames(goEntries, frrData, birdData, canonical)
	annotations := reportAnnotations()
	benchmarks := make([]benchmark, 0, len(ordered))
	for _, name := range ordered {
		benchmarks = append(benchmarks, buildBenchmark(name, reverse, goAverage, frrData, birdData, annotations))
	}
	return benchmarks
}

func reverseCanonicalNames(goAverage map[string]goMetric, canonical map[string]string) map[string]string {
	reverse := make(map[string]string, len(goAverage))
	for original := range goAverage {
		name := canonicalBenchmarkName(original, canonical)
		reverse[name] = original
	}
	return reverse
}

func orderedBenchmarkNames(
	goEntries []goEntry,
	frrData cInput,
	birdData cInput,
	canonical map[string]string,
) []string {
	ordered := make([]string, 0, len(goEntries)+len(frrData.order)+len(birdData.order))
	seen := make(map[string]struct{})
	for _, entry := range goEntries {
		ordered = appendUniqueName(ordered, seen, canonicalBenchmarkName(entry.name, canonical))
	}
	for _, name := range frrData.order {
		ordered = appendUniqueName(ordered, seen, name)
	}
	for _, name := range birdData.order {
		ordered = appendUniqueName(ordered, seen, name)
	}
	return ordered
}

func appendUniqueName(names []string, seen map[string]struct{}, name string) []string {
	if _, exists := seen[name]; exists {
		return names
	}
	seen[name] = struct{}{}
	return append(names, name)
}

func canonicalBenchmarkName(name string, canonical map[string]string) string {
	if mapped, ok := canonical[name]; ok {
		return mapped
	}
	return name
}

func buildBenchmark(
	name string,
	reverse map[string]string,
	goAverage map[string]goMetric,
	frrData cInput,
	birdData cInput,
	annotations map[string]annotation,
) benchmark {
	entry := benchmark{Name: name}
	original := name
	if mapped, ok := reverse[name]; ok {
		original = mapped
	}
	if metric, ok := goAverage[original]; ok {
		goValue := metric
		entry.Go = &goValue
	}
	if value, ok := frrData.averages[name]; ok {
		entry.FRR = &cMetric{NSOp: value}
	}
	if value, ok := birdData.averages[name]; ok {
		entry.BIRD = &cMetric{NSOp: value}
	}
	if value, ok := annotations[name]; ok {
		annotationValue := value
		entry.Annotation = &annotationValue
	}
	return entry
}

func canonicalNames() map[string]string {
	return map[string]string{
		"ControlPacketMarshal":           "Marshal",
		"ControlPacketMarshalWithAuth":   "MarshalWithAuth",
		"ControlPacketUnmarshal":         "Unmarshal",
		"ControlPacketUnmarshalWithAuth": "UnmarshalWithAuth",
		"ControlPacketRoundTrip":         "RoundTrip",
		"FSMTransitionUpRecvUp":          "FSMUpRecvUp",
		"FSMTransitionDownRecvDown":      "FSMDownRecvDown",
		"FSMTransitionUpTimerExpired":    "FSMUpTimerExpired",
		"FSMTransitionIgnored":           "FSMIgnored",
		"ApplyJitter":                    "Jitter",
		"ApplyJitterDetectMultOne":       "JitterDetectMultOne",
		"PacketPool":                     "PacketPool",
		"RecvStateToEvent":               "RecvStateToEvent",
		"FullRecvPath":                   "FullRxPath",
		"FullTxPath":                     "FullTxPath",
		"SessionRecvPacket":              "SessionRecvPacket",
		"DetectionTimeCalc":              "DetectionTimeCalc",
		"CalcTxInterval":                 "CalcTxInterval",
		"ManagerCreate100Sessions":       "ManagerCreate100Sessions",
		"ManagerCreate1000Sessions":      "ManagerCreate1000Sessions",
		"ManagerDemux1000Sessions":       "ManagerDemux1000Sessions",
		"ManagerReconcile":               "ManagerReconcile",
		"SessionApplyJitter":             "SessionApplyJitter",
		"DetectionTimeCalcHot":           "DetectionTimeCalcHot",
		"CalcTxIntervalHot":              "CalcTxIntervalHot",
		"FullRecvPathCodec":              "FullRxPathCodec",
		"ManagerLookup1000Sessions":      "SessionLookup1000",
	}
}

func reportAnnotations() map[string]annotation {
	return map[string]annotation{
		"MarshalWithAuth":           {GoOnly: "Requires crypto (SHA1 HMAC) not in bench container"},
		"UnmarshalWithAuth":         {GoOnly: "Requires crypto (SHA1 HMAC) not in bench container"},
		"PacketPool":                {GoOnly: "sync.Pool is Go GC-specific; C has no GC"},
		"SessionRecvPacket":         {GoOnly: "Measures goroutine channel send; C uses poll()"},
		"SessionApplyJitter":        {GoOnly: "Session-local PCG PRNG optimization; unique to Go goroutine model"},
		"JitterDetectMultOne":       {GoOnly: "DetectMult=1 variant of global jitter"},
		"ManagerReconcile":          {GoOnly: "Config reconciliation pattern unique to Go Manager"},
		"ManagerCreate100Sessions":  {GoOnly: "Go Manager with goroutine-per-session; not comparable to C"},
		"ManagerCreate1000Sessions": {GoOnly: "Go Manager with goroutine-per-session; not comparable to C"},
		"ManagerDemux1000Sessions":  {GoOnly: "Go Manager discriminator map + channel send"},
		"DetectionTimeCalcHot":      {GoOnly: "Uses cachedState (goroutine-confined); no atomic load"},
		"CalcTxIntervalHot":         {GoOnly: "Uses cachedState (goroutine-confined); no atomic load"},
		"Unmarshal":                 {Note: "Go: 7 RFC 5880 §6.8.6 validation checks; C: 3 checks"},
		"DetectionTimeCalc":         {Note: "Go uses atomic State() load; hot path uses cachedState"},
		"CalcTxInterval":            {Note: "Go uses atomic State() load; hot path uses cachedState"},
		"Jitter":                    {Note: "Go global PRNG (atomic); session-local PRNG avoids contention"},
		"FullRxPath": {
			Note: "UNFAIR: Go = unmarshal + RWMutex + map + channel send; " +
				"C = unmarshal + FSM (no IPC). See FullRxPathCodec for fair comparison",
		},
		"FullRxPathCodec": {GoOnly: "Codec-only RX path (unmarshal + FSM); fair comparison with C FullRxPath"},
		"SessionDemux1000": {
			Note: "UNFAIR: Go = RWMutex + map + channel send; " +
				"C = hashmap lookup only. See SessionLookup1000 for fair comparison",
		},
		"SessionCreate1000": {
			Note: "DIFFERENT OPS: Go creates goroutines + channels + context; " +
				"C allocates structs + hashmap insert",
		},
		"SessionLookup1000": {
			GoOnly: "Pure RWMutex + map lookup (no channel send); " +
				"fair comparison with C SessionDemux1000",
		},
	}
}

func reportFeatures() []feature {
	return []feature{
		{Name: "RFC 5880 (BFD Base)", Go: true, FRR: true, BIRD: true},
		{Name: "RFC 5881 (IPv4/IPv6)", Go: true, FRR: true, BIRD: true},
		{Name: "Keyed SHA1 Auth", Go: true, FRR: true, BIRD: true},
		{Name: "Keyed MD5 Auth", Go: true, FRR: true, BIRD: true},
		{Name: "VXLAN BFD (RFC 8971)", Go: true, FRR: false, BIRD: false},
		{Name: "Geneve BFD (RFC 9521)", Go: true, FRR: false, BIRD: false},
		{Name: "Multihop (RFC 5883)", Go: true, FRR: true, BIRD: true},
		{Name: "Micro-BFD (RFC 7130)", Go: true, FRR: false, BIRD: false},
		{Name: "CPI Bit", Go: true, FRR: "partial", BIRD: false},
		{Name: "Zero-alloc Hot Path", Go: true, FRR: "n/a", BIRD: "n/a"},
		{Name: "Goroutine-per-Session", Go: true, FRR: false, BIRD: false},
		{Name: "gRPC / ConnectRPC API", Go: true, FRR: false, BIRD: false},
		{Name: "Prometheus Metrics", Go: true, FRR: false, BIRD: false},
	}
}

func oneDecimal(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

func writeReport(ctx context.Context, templatePath, outputPath string, document report) error {
	template, found, err := readRegularNonempty(templatePath, "report template", false)
	if err != nil {
		return err
	}
	if !found {
		return invalidInputf("required report template does not exist: %s", templatePath)
	}
	if bytes.Count(template, []byte(reportMarker)) != 1 {
		return invalidInputf("%s: report template must contain exactly one data marker", templatePath)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode benchmark report: %w", err)
	}
	rendered := bytes.Replace(template, []byte(reportMarker), encoded, 1)
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("publish benchmark report: %w", contextErr)
	}
	if err := writeAtomic(outputPath, rendered); err != nil {
		return fmt.Errorf("publish benchmark report: %w", err)
	}
	return nil
}

type fileSnapshot struct {
	info   os.FileInfo
	exists bool
}

func writeAtomic(path string, data []byte) (returnErr error) {
	if path == "" {
		return invalidOutputf("report output path is empty")
	}
	directory := filepath.Dir(path)
	base := filepath.Base(path)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open report output directory %s: %w", directory, err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeRoot(root, "report output directory"))
	}()
	initial, err := snapshotFile(root, base, path)
	if err != nil {
		return err
	}
	temporary, temporaryName, err := createTemporary(root, base)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			returnErr = errors.Join(returnErr, removeRootFile(root, temporaryName))
		}
	}()
	temporaryInfo, err := finishTemporary(temporary, data)
	if err != nil {
		return err
	}
	renamed, err = publishTemporary(root, initial, temporaryInfo, temporaryName, base, path)
	return err
}

func finishTemporary(temporary *os.File, data []byte) (os.FileInfo, error) {
	if _, err := temporary.Write(data); err != nil {
		return nil, errors.Join(
			fmt.Errorf("write temporary report: %w", err),
			closeFile(temporary, "temporary report after write failure"),
		)
	}
	if err := temporary.Sync(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("sync temporary report: %w", err),
			closeFile(temporary, "temporary report after sync failure"),
		)
	}
	temporaryInfo, err := temporary.Stat()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("stat temporary report: %w", err),
			closeFile(temporary, "temporary report after stat failure"),
		)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close temporary report: %w", err)
	}
	return temporaryInfo, nil
}

func publishTemporary(
	root *os.Root,
	initial fileSnapshot,
	temporaryInfo os.FileInfo,
	temporaryName string,
	base string,
	path string,
) (bool, error) {
	current, err := snapshotFile(root, base, path)
	if err != nil {
		return false, err
	}
	if !sameSnapshot(initial, current) {
		return false, invalidOutputf("report output changed before atomic rename: %s", path)
	}
	if err := root.Rename(temporaryName, base); err != nil {
		return false, fmt.Errorf("atomically replace report output %s: %w", path, err)
	}
	if err := validatePublished(root, base, path, temporaryInfo); err != nil {
		return true, err
	}
	return true, nil
}

func validatePublished(root *os.Root, base, path string, temporaryInfo os.FileInfo) error {
	published, err := root.Lstat(base)
	if err != nil {
		return fmt.Errorf("lstat published report %s: %w", path, err)
	}
	if !published.Mode().IsRegular() || published.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(temporaryInfo, published) {
		return invalidOutputf("published report is not the rooted temporary file: %s", path)
	}
	if published.Mode().Perm() != reportFileMode {
		return invalidOutputf(
			"published report mode is %04o, want 0600: %s",
			published.Mode().Perm(),
			path,
		)
	}
	return nil
}

func snapshotFile(root *os.Root, name, path string) (fileSnapshot, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("lstat report output %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fileSnapshot{}, invalidOutputf("report output must be a regular file path: %s", path)
	}
	return fileSnapshot{info: info, exists: true}, nil
}

func sameSnapshot(first, second fileSnapshot) bool {
	if !first.exists || !second.exists {
		return first.exists == second.exists
	}
	return os.SameFile(first.info, second.info)
}

func createTemporary(root *os.Root, base string) (*os.File, string, error) {
	for range maximumTempAttempts {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", fmt.Errorf("generate temporary report name: %w", err)
		}
		name := "." + base + "." + hex.EncodeToString(random[:]) + ".tmp"
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, reportFileMode)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create rooted temporary report: %w", err)
		}
		if err := file.Chmod(reportFileMode); err != nil {
			return nil, "", errors.Join(
				fmt.Errorf("chmod rooted temporary report: %w", err),
				closeFile(file, "temporary report after chmod failure"),
				removeRootFile(root, name),
			)
		}
		return file, name, nil
	}
	return nil, "", invalidOutputf("create rooted temporary report after %d attempts", maximumTempAttempts)
}

func readRegularNonempty(path, label string, optional bool) ([]byte, bool, error) {
	if path == "" {
		return nil, false, invalidInputf("%s path is empty", label)
	}
	directory := filepath.Dir(path)
	base := filepath.Base(path)
	root, err := os.OpenRoot(directory)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("open parent directory for %s %s: %w", label, path, err)
	}
	data, found, readErr := readRegularFromRoot(root, base, path, label, optional)
	closeErr := closeRoot(root, label+" parent directory")
	return data, found, errors.Join(readErr, closeErr)
}

func readRegularFromRoot(root *os.Root, base, path, label string, optional bool) ([]byte, bool, error) {
	initial, err := root.Lstat(base)
	if errors.Is(err, os.ErrNotExist) && optional {
		return nil, false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, invalidInputf("required %s does not exist: %s", label, path)
	}
	if err != nil {
		return nil, false, fmt.Errorf("lstat %s %s: %w", label, path, err)
	}
	if validationErr := validateInitialInput(initial, path, label); validationErr != nil {
		return nil, false, validationErr
	}
	file, err := root.Open(base)
	if err != nil {
		return nil, false, fmt.Errorf("open %s %s: %w", label, path, err)
	}
	data, err := readOpenedRegular(root, file, initial, base, path, label)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func validateInitialInput(info os.FileInfo, path, label string) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return invalidInputf("%s must not be a symlink: %s", label, path)
	}
	if !info.Mode().IsRegular() {
		return invalidInputf("%s is not a regular file: %s", label, path)
	}
	if info.Size() == 0 {
		return invalidInputf("%s is empty: %s", label, path)
	}
	if info.Size() > maxInputSize {
		return invalidInputf("%s is %d bytes, limit is %d: %s", label, info.Size(), maxInputSize, path)
	}
	return nil
}

func readOpenedRegular(
	root *os.Root,
	file *os.File,
	initial os.FileInfo,
	base string,
	path string,
	label string,
) ([]byte, error) {
	opened, err := file.Stat()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("stat opened %s %s: %w", label, path, err),
			closeFile(file, label+" after stat failure"),
		)
	}
	current, err := root.Lstat(base)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("lstat opened %s %s: %w", label, path, err),
			closeFile(file, label+" after second lstat failure"),
		)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(initial, opened) || !os.SameFile(current, opened) {
		return nil, errors.Join(
			fmt.Errorf("%w: %s changed while opening or is unsafe: %s", errUnsafeInput, label, path),
			closeFile(file, "unsafe "+label),
		)
	}
	return readBoundedUTF8(file, path, label)
}

func readBoundedUTF8(file *os.File, path, label string) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(file, maxInputSize+1))
	closeErr := closeFile(file, label)
	if readErr != nil {
		return nil, errors.Join(fmt.Errorf("read bounded %s %s: %w", label, path, readErr), closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > maxInputSize {
		return nil, invalidInputf("%s grew beyond limit %d: %s", label, maxInputSize, path)
	}
	if !utf8.Valid(data) {
		return nil, invalidInputf("%s is not valid UTF-8: %s", label, path)
	}
	return data, nil
}

func invalidInputf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidInput, fmt.Sprintf(format, args...))
}

func invalidOutputf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidOutput, fmt.Sprintf(format, args...))
}

func removeRootFile(root *os.Root, name string) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove temporary report %s: %w", name, err)
	}
	return nil
}

func closeFile(file *os.File, description string) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", description, err)
	}
	return nil
}

func closeRoot(root *os.Root, description string) error {
	if err := root.Close(); err != nil {
		return fmt.Errorf("close %s: %w", description, err)
	}
	return nil
}

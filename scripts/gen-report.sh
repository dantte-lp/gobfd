#!/usr/bin/env bash
# gen-report.sh — Validate cross-implementation benchmark results and generate
# an interactive Alpine.js HTML report.
#
# Usage:
#   ./scripts/gen-report.sh [RESULTS_DIR]
#
# Required input files:
#   bench-go.txt      — Go benchmark output (go test -bench format)
#   bench-c-frr.txt   — C FRR-style records (BENCH format)
#   bench-c-bird.txt  — C BIRD-style records (BENCH format)
#
# Optional environment:
#   BENCH_REPORT_OUTPUT — output HTML path
#   BENCH_META_JSON     — benchmark metadata JSON path
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
RESULTS_DIR="${1:-${PROJECT_DIR}/bench-results}"
TEMPLATE="${SCRIPT_DIR}/report-template.html"
OUTPUT="${BENCH_REPORT_OUTPUT:-${PROJECT_DIR}/reports/benchmarks/cross-comparison.html}"
META_JSON="${BENCH_META_JSON:-${PROJECT_DIR}/testdata/benchmarks/v0.4.0/meta.json}"

GO_INPUT="${RESULTS_DIR}/bench-go.txt"
FRR_INPUT="${RESULTS_DIR}/bench-c-frr.txt"
BIRD_INPUT="${RESULTS_DIR}/bench-c-bird.txt"

require_regular_nonempty() {
    local label="$1"
    local path="$2"
    if [ -L "${path}" ]; then
        printf 'gen-report: %s must not be a symlink: %s\n' "${label}" "${path}" >&2
        return 1
    fi
    if [ ! -f "${path}" ]; then
        printf 'gen-report: required %s is not a regular file: %s\n' "${label}" "${path}" >&2
        return 1
    fi
    if [ ! -s "${path}" ]; then
        printf 'gen-report: required %s is empty: %s\n' "${label}" "${path}" >&2
        return 1
    fi
}

require_regular_nonempty "Go benchmark input" "${GO_INPUT}"
require_regular_nonempty "FRR benchmark input" "${FRR_INPUT}"
require_regular_nonempty "BIRD benchmark input" "${BIRD_INPUT}"
require_regular_nonempty "report template" "${TEMPLATE}"

OUTPUT_DIR="$(dirname -- "${OUTPUT}")"
mkdir -p "${OUTPUT_DIR}"

GENERATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PLATFORM=""
GOARCH="$(awk '/^goarch:/ { print $2; exit }' "${GO_INPUT}")"
GOOS="$(awk '/^goos:/ { print $2; exit }' "${GO_INPUT}")"
CPU="$(awk '/^cpu:/ { $1=""; print substr($0,2); exit }' "${GO_INPUT}")"
if [ -n "${CPU}" ]; then
    PLATFORM="${CPU}"
fi
if [ -n "${GOARCH}" ] && [ -n "${GOOS}" ]; then
    PLATFORM="${PLATFORM:+${PLATFORM}, }${GOOS}/${GOARCH}"
fi
if [ -z "${PLATFORM}" ]; then
    PLATFORM="unknown"
fi

export GENERATED PLATFORM
export GCC_VERSION="gcc (Debian trixie)"
export GOMAXPROCS="${GOMAXPROCS:-8}"

python3 - "${GO_INPUT}" "${FRR_INPUT}" "${BIRD_INPUT}" \
    "${META_JSON}" "${TEMPLATE}" "${OUTPUT}" <<'PY'
import json
import math
import os
from collections import defaultdict
from pathlib import Path
import re
import stat
import sys
import tempfile


def read_regular_nonempty(path_text, label, *, optional=False):
    path = Path(path_text)
    try:
        info = path.lstat()
    except FileNotFoundError:
        if optional:
            return None
        raise ValueError(f"required {label} does not exist: {path}") from None
    if stat.S_ISLNK(info.st_mode):
        raise ValueError(f"{label} must not be a symlink: {path}")
    if not stat.S_ISREG(info.st_mode):
        raise ValueError(f"{label} is not a regular file: {path}")
    if info.st_size == 0:
        raise ValueError(f"{label} is empty: {path}")
    try:
        return path.read_text(encoding="utf-8")
    except UnicodeError as error:
        raise ValueError(f"{label} is not valid UTF-8: {path}: {error}") from error


def non_negative_float(value, location):
    try:
        number = float(value)
    except ValueError as error:
        raise ValueError(f"{location}: invalid number {value!r}") from error
    if not math.isfinite(number):
        raise ValueError(f"{location}: value must be finite, got {value!r}")
    if number < 0:
        raise ValueError(f"{location}: value must be non-negative, got {value!r}")
    return number


def positive_integer(value, location):
    try:
        number = int(value)
    except ValueError as error:
        raise ValueError(f"{location}: invalid integer {value!r}") from error
    if number <= 0:
        raise ValueError(f"{location}: value must be positive, got {value!r}")
    return number


def allocation_metric(value, location):
    number = non_negative_float(value, location)
    if not number.is_integer():
        raise ValueError(f"{location}: allocation metric must be an integer, got {value!r}")
    return int(number)


def parse_go(path_text):
    text = read_regular_nonempty(path_text, "Go benchmark input")
    entries = []
    for line_number, line in enumerate(text.splitlines(), start=1):
        stripped = line.strip()
        if not stripped.startswith("Benchmark"):
            if not stripped or stripped == "PASS" or stripped.startswith(
                ("goos:", "goarch:", "pkg:", "cpu:", "ok\t", "ok  ")
            ):
                continue
            raise ValueError(f"{path_text}:{line_number}: malformed Go benchmark output")
        fields = stripped.split()
        location = f"{path_text}:{line_number}"
        if len(fields) < 4 or (len(fields) - 2) % 2 != 0:
            raise ValueError(f"{location}: malformed Go benchmark record")
        positive_integer(fields[1], f"{location} iterations")
        metrics = {}
        for index in range(2, len(fields), 2):
            unit = fields[index + 1]
            if unit in metrics:
                raise ValueError(f"{location}: duplicate metric {unit!r}")
            metrics[unit] = non_negative_float(fields[index], f"{location} {unit}")
        if "ns/op" not in metrics:
            raise ValueError(f"{location}: missing ns/op metric")

        name = re.sub(r"-[0-9]+$", "", fields[0]).removeprefix("Benchmark")
        if not name:
            raise ValueError(f"{location}: benchmark name is empty")
        entry = {"name": name, "ns_op": metrics["ns/op"]}
        if "B/op" in metrics:
            entry["b_op"] = allocation_metric(
                fields[fields.index("B/op") - 1], f"{location} B/op"
            )
        if "allocs/op" in metrics:
            entry["allocs_op"] = allocation_metric(
                fields[fields.index("allocs/op") - 1], f"{location} allocs/op"
            )
        entries.append(entry)
    if not entries:
        raise ValueError(f"{path_text}: no valid Go benchmark records")
    return entries


def parse_c(path_text, implementation):
    text = read_regular_nonempty(path_text, f"{implementation} benchmark input")
    sums = defaultdict(float)
    counts = defaultdict(int)
    for line_number, line in enumerate(text.splitlines(), start=1):
        location = f"{path_text}:{line_number}"
        if not line:
            raise ValueError(f"{location}: empty benchmark record")
        fields = line.split("\t")
        if len(fields) != 5 or fields[0] != "BENCH":
            raise ValueError(f"{location}: malformed BENCH record")
        if fields[1] != implementation:
            raise ValueError(
                f"{location}: implementation is {fields[1]!r}, expected {implementation!r}"
            )
        if not fields[2]:
            raise ValueError(f"{location}: benchmark name is empty")
        duration = non_negative_float(fields[3], f"{location} ns/op")
        positive_integer(fields[4], f"{location} iterations")
        sums[fields[2]] += duration
        counts[fields[2]] += 1
    if not counts:
        raise ValueError(f"{path_text}: no valid {implementation} benchmark records")
    return {name: round(sums[name] / counts[name], 3) for name in sums}


def load_metadata(path_text):
    text = read_regular_nonempty(path_text, "benchmark metadata JSON", optional=True)
    if text is None:
        return {"version": "unknown", "go": "unknown"}
    try:
        metadata = json.loads(text)
    except json.JSONDecodeError as error:
        raise ValueError(f"{path_text}: malformed benchmark metadata JSON: {error}") from error
    if not isinstance(metadata, dict):
        raise ValueError(f"{path_text}: benchmark metadata JSON must be an object")

    def optional_non_empty_string(key):
        if key not in metadata:
            return "unknown"
        value = metadata[key]
        if not isinstance(value, str) or not value.strip():
            raise ValueError(
                f"{path_text}: benchmark metadata {key!r} must be a non-empty string"
            )
        return value

    return {
        "version": optional_non_empty_string("version"),
        "go": optional_non_empty_string("go"),
    }


def build_report(go_entries, frr_data, bird_data, metadata):
    go_sum = defaultdict(lambda: {"ns_sum": 0.0, "count": 0, "b_op": 0, "allocs_op": 0})
    for entry in go_entries:
        value = go_sum[entry["name"]]
        value["ns_sum"] += entry["ns_op"]
        value["count"] += 1
        value["b_op"] = entry.get("b_op", 0)
        value["allocs_op"] = entry.get("allocs_op", 0)
    go_average = {
        name: {
            "ns_op": round(value["ns_sum"] / value["count"], 3),
            "b_op": value["b_op"],
            "allocs_op": value["allocs_op"],
        }
        for name, value in go_sum.items()
    }

    canonical = {
        "ControlPacketMarshal": "Marshal",
        "ControlPacketMarshalWithAuth": "MarshalWithAuth",
        "ControlPacketUnmarshal": "Unmarshal",
        "ControlPacketUnmarshalWithAuth": "UnmarshalWithAuth",
        "ControlPacketRoundTrip": "RoundTrip",
        "FSMTransitionUpRecvUp": "FSMUpRecvUp",
        "FSMTransitionDownRecvDown": "FSMDownRecvDown",
        "FSMTransitionUpTimerExpired": "FSMUpTimerExpired",
        "FSMTransitionIgnored": "FSMIgnored",
        "ApplyJitter": "Jitter",
        "ApplyJitterDetectMultOne": "JitterDetectMultOne",
        "PacketPool": "PacketPool",
        "RecvStateToEvent": "RecvStateToEvent",
        "FullRecvPath": "FullRxPath",
        "FullTxPath": "FullTxPath",
        "SessionRecvPacket": "SessionRecvPacket",
        "DetectionTimeCalc": "DetectionTimeCalc",
        "CalcTxInterval": "CalcTxInterval",
        "ManagerCreate100Sessions": "ManagerCreate100Sessions",
        "ManagerCreate1000Sessions": "ManagerCreate1000Sessions",
        "ManagerDemux1000Sessions": "ManagerDemux1000Sessions",
        "ManagerReconcile": "ManagerReconcile",
        "SessionApplyJitter": "SessionApplyJitter",
        "DetectionTimeCalcHot": "DetectionTimeCalcHot",
        "CalcTxIntervalHot": "CalcTxIntervalHot",
        "FullRecvPathCodec": "FullRxPathCodec",
        "ManagerLookup1000Sessions": "SessionLookup1000",
    }
    reverse = {canonical.get(original, original): original for original in go_average}
    ordered_names = []
    seen = set()
    for entry in go_entries:
        name = canonical.get(entry["name"], entry["name"])
        if name not in seen:
            seen.add(name)
            ordered_names.append(name)
    for implementation in (frr_data, bird_data):
        for name in implementation:
            if name not in seen:
                seen.add(name)
                ordered_names.append(name)

    benchmarks = []
    for name in ordered_names:
        benchmark = {"name": name}
        original = reverse.get(name, name)
        if original in go_average:
            benchmark["go"] = go_average[original]
        if name in frr_data:
            benchmark["frr"] = {"ns_op": frr_data[name]}
        if name in bird_data:
            benchmark["bird"] = {"ns_op": bird_data[name]}
        benchmarks.append(benchmark)

    annotations = {
        "MarshalWithAuth": {"go_only": "Requires crypto (SHA1 HMAC) not in bench container"},
        "UnmarshalWithAuth": {"go_only": "Requires crypto (SHA1 HMAC) not in bench container"},
        "PacketPool": {"go_only": "sync.Pool is Go GC-specific; C has no GC"},
        "SessionRecvPacket": {"go_only": "Measures goroutine channel send; C uses poll()"},
        "SessionApplyJitter": {"go_only": "Session-local PCG PRNG optimization; unique to Go goroutine model"},
        "JitterDetectMultOne": {"go_only": "DetectMult=1 variant of global jitter"},
        "ManagerReconcile": {"go_only": "Config reconciliation pattern unique to Go Manager"},
        "ManagerCreate100Sessions": {"go_only": "Go Manager with goroutine-per-session; not comparable to C"},
        "ManagerCreate1000Sessions": {"go_only": "Go Manager with goroutine-per-session; not comparable to C"},
        "ManagerDemux1000Sessions": {"go_only": "Go Manager discriminator map + channel send"},
        "DetectionTimeCalcHot": {"go_only": "Uses cachedState (goroutine-confined); no atomic load"},
        "CalcTxIntervalHot": {"go_only": "Uses cachedState (goroutine-confined); no atomic load"},
        "Unmarshal": {"note": "Go: 7 RFC 5880 \u00a76.8.6 validation checks; C: 3 checks"},
        "DetectionTimeCalc": {"note": "Go uses atomic State() load; hot path uses cachedState"},
        "CalcTxInterval": {"note": "Go uses atomic State() load; hot path uses cachedState"},
        "Jitter": {"note": "Go global PRNG (atomic); session-local PRNG avoids contention"},
        "FullRxPath": {"note": "UNFAIR: Go = unmarshal + RWMutex + map + channel send; C = unmarshal + FSM (no IPC). See FullRxPathCodec for fair comparison"},
        "FullRxPathCodec": {"go_only": "Codec-only RX path (unmarshal + FSM); fair comparison with C FullRxPath"},
        "SessionDemux1000": {"note": "UNFAIR: Go = RWMutex + map + channel send; C = hashmap lookup only. See SessionLookup1000 for fair comparison"},
        "SessionCreate1000": {"note": "DIFFERENT OPS: Go creates goroutines + channels + context; C allocates structs + hashmap insert"},
        "SessionLookup1000": {"go_only": "Pure RWMutex + map lookup (no channel send); fair comparison with C SessionDemux1000"},
    }
    for benchmark in benchmarks:
        if benchmark["name"] in annotations:
            benchmark["annotation"] = annotations[benchmark["name"]]

    if "ControlPacketMarshal" not in go_average:
        raise ValueError("Go benchmark input is missing required ControlPacketMarshal headline")
    if "Marshal" not in frr_data:
        raise ValueError("FRR benchmark input is missing required FRR Marshal headline")
    if "Marshal" not in bird_data:
        raise ValueError("BIRD benchmark input is missing required BIRD Marshal headline")
    go_marshal = go_average["ControlPacketMarshal"]["ns_op"]
    frr_marshal = frr_data["Marshal"]
    bird_marshal = bird_data["Marshal"]
    return {
        "meta": {
            "generated": os.environ.get("GENERATED", ""),
            "gobfd_version": metadata["version"],
            "go_version": metadata["go"],
            "gcc_version": os.environ.get("GCC_VERSION", ""),
            "platform": os.environ.get("PLATFORM", ""),
            "gomaxprocs": os.environ.get("GOMAXPROCS", "8"),
        },
        "implementations": [
            {"id": "go", "name": "GoBFD (Go)", "short_name": "Go", "color": "#00d4ff", "has_allocs": True, "headline_value": f"{go_marshal:.1f}", "headline_unit": "ns/op", "headline_label": "Packet Marshal"},
            {"id": "frr", "name": "C (FRR bfdd)", "short_name": "C-FRR", "color": "#4ade80", "has_allocs": False, "headline_value": f"{frr_marshal:.1f}", "headline_unit": "ns/op", "headline_label": "Marshal (stack alloc)"},
            {"id": "bird", "name": "C (BIRD BFD)", "short_name": "C-BIRD", "color": "#fbbf24", "has_allocs": False, "headline_value": f"{bird_marshal:.1f}", "headline_unit": "ns/op", "headline_label": "Marshal (pre-alloc)"},
        ],
        "benchmarks": benchmarks,
        "features": [
            {"name": "RFC 5880 (BFD Base)", "go": True, "frr": True, "bird": True},
            {"name": "RFC 5881 (IPv4/IPv6)", "go": True, "frr": True, "bird": True},
            {"name": "Keyed SHA1 Auth", "go": True, "frr": True, "bird": True},
            {"name": "Keyed MD5 Auth", "go": True, "frr": True, "bird": True},
            {"name": "VXLAN BFD (RFC 8971)", "go": True, "frr": False, "bird": False},
            {"name": "Geneve BFD (RFC 9521)", "go": True, "frr": False, "bird": False},
            {"name": "Multihop (RFC 5883)", "go": True, "frr": True, "bird": True},
            {"name": "Micro-BFD (RFC 7130)", "go": True, "frr": False, "bird": False},
            {"name": "CPI Bit", "go": True, "frr": "partial", "bird": False},
            {"name": "Zero-alloc Hot Path", "go": True, "frr": "n/a", "bird": "n/a"},
            {"name": "Goroutine-per-Session", "go": True, "frr": False, "bird": False},
            {"name": "gRPC / ConnectRPC API", "go": True, "frr": False, "bird": False},
            {"name": "Prometheus Metrics", "go": True, "frr": False, "bird": False},
        ],
    }


def write_report(template_path_text, output_path_text, report):
    template = read_regular_nonempty(template_path_text, "report template")
    marker = "__BENCHMARK_DATA__"
    if template.count(marker) != 1:
        raise ValueError(f"{template_path_text}: report template must contain exactly one data marker")
    output_path = Path(output_path_text)
    try:
        info = output_path.lstat()
    except FileNotFoundError:
        pass
    else:
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise ValueError(f"report output must be a regular file path: {output_path}")
    encoded = json.dumps(report, separators=(",", ":"), allow_nan=False)
    rendered = template.replace(marker, encoded)
    temporary_path = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            prefix=f".{output_path.name}.",
            suffix=".tmp",
            dir=output_path.parent,
            delete=False,
        ) as temporary:
            temporary_path = Path(temporary.name)
            temporary.write(rendered)
            temporary.flush()
            os.fsync(temporary.fileno())
        os.replace(temporary_path, output_path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            try:
                temporary_path.unlink()
            except FileNotFoundError:
                pass


def main():
    if len(sys.argv) != 7:
        raise ValueError("internal argument contract requires six paths")
    go_path, frr_path, bird_path, meta_path, template_path, output_path = sys.argv[1:]
    report = build_report(
        parse_go(go_path),
        parse_c(frr_path, "frr"),
        parse_c(bird_path, "bird"),
        load_metadata(meta_path),
    )
    write_report(template_path, output_path, report)


try:
    main()
except (OSError, ValueError) as error:
    print(f"gen-report: {error}", file=sys.stderr)
    raise SystemExit(1) from None
PY

printf '=== Cross-comparison report: %s ===\n' "${OUTPUT}"
printf '%s\n' '=== Open in browser to view interactive results ==='

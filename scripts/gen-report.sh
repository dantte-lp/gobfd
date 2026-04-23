#!/usr/bin/env bash
# gen-report.sh — Collect cross-implementation benchmark results and generate
# an interactive Alpine.js HTML report.
#
# Usage:
#   ./scripts/gen-report.sh [RESULTS_DIR]
#
# Input files (in RESULTS_DIR):
#   bench-go.txt           — Go benchmark output (go test -bench format)
#   bench-c-frr.txt        — C FRR-style (BENCH format)
#   bench-c-bird.txt       — C BIRD-style (BENCH format)
#   bench-python-aiobfd.txt — Python aiobfd-style (BENCH format)
#   bench-python-struct.txt — Python struct-style (BENCH format)
#
# Output:
#   reports/benchmarks/cross-comparison.html
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
RESULTS_DIR="${1:-${PROJECT_DIR}/bench-results}"
TEMPLATE="${SCRIPT_DIR}/report-template.html"
OUTPUT_DIR="${PROJECT_DIR}/reports/benchmarks"
OUTPUT="${OUTPUT_DIR}/cross-comparison.html"

mkdir -p "${OUTPUT_DIR}"

# -----------------------------------------------------------------------
# Parse Go benchmark output
# Input:  BenchmarkControlPacketMarshal-8   195605820   5.869 ns/op   0 B/op   0 allocs/op
# Output: JSON fragments
# -----------------------------------------------------------------------
parse_go() {
    local file="${RESULTS_DIR}/bench-go.txt"
    if [ ! -f "$file" ]; then echo "[]"; return; fi

    awk '
    /^Benchmark[A-Z]/ {
        name = $1
        sub(/-[0-9]+$/, "", name)    # strip -8
        sub(/^Benchmark/, "", name)   # strip Benchmark prefix

        ns = ""
        bop = ""
        allocs = ""
        for (i = 2; i <= NF; i++) {
            if ($(i+1) == "ns/op") ns = $i
            if ($(i+1) == "B/op") bop = $i
            if ($(i+1) == "allocs/op") allocs = $i
        }
        if (ns != "") {
            printf "{\"name\":\"%s\",\"ns_op\":%s", name, ns
            if (bop != "") printf ",\"b_op\":%s", bop
            if (allocs != "") printf ",\"allocs_op\":%s", allocs
            printf "}\n"
        }
    }' "$file"
}

# -----------------------------------------------------------------------
# Parse C/Python BENCH format
# Input:  BENCH<tab>impl<tab>name<tab>ns_per_op<tab>iterations
# Output: JSON fragments (averaged over repeats)
# -----------------------------------------------------------------------
parse_bench() {
    local file="$1"
    if [ ! -f "$file" ]; then echo ""; return; fi

    awk -F'\t' '
    $1 == "BENCH" {
        sum[$3] += $4
        count[$3]++
    }
    END {
        for (name in sum) {
            avg = sum[name] / count[name]
            printf "{\"name\":\"%s\",\"ns_op\":%.3f}\n", name, avg
        }
    }' "$file"
}

# -----------------------------------------------------------------------
# Normalize Go benchmark names to canonical names
# ControlPacketMarshal → Marshal, FSMTransitionUpRecvUp → FSMUpRecvUp, etc.
# -----------------------------------------------------------------------
normalize_go_name() {
    local name="$1"
    case "$name" in
        ControlPacketMarshal)            echo "Marshal" ;;
        ControlPacketMarshalWithAuth)    echo "MarshalWithAuth" ;;
        ControlPacketUnmarshal)          echo "Unmarshal" ;;
        ControlPacketUnmarshalWithAuth)  echo "UnmarshalWithAuth" ;;
        ControlPacketRoundTrip)          echo "RoundTrip" ;;
        FSMTransitionUpRecvUp)           echo "FSMUpRecvUp" ;;
        FSMTransitionDownRecvDown)       echo "FSMDownRecvDown" ;;
        FSMTransitionUpTimerExpired)     echo "FSMUpTimerExpired" ;;
        FSMTransitionIgnored)            echo "FSMIgnored" ;;
        ApplyJitter)                     echo "Jitter" ;;
        ApplyJitterDetectMultOne)        echo "JitterDetectMultOne" ;;
        PacketPool)                      echo "PacketPool" ;;
        SessionApplyJitter)              echo "SessionApplyJitter" ;;
        DetectionTimeCalcHot)            echo "DetectionTimeCalcHot" ;;
        CalcTxIntervalHot)               echo "CalcTxIntervalHot" ;;
        *)                               echo "$name" ;;
    esac
}

# -----------------------------------------------------------------------
# Collect metadata
# -----------------------------------------------------------------------
GENERATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GOBFD_VERSION="unknown"
GO_VERSION="unknown"
GCC_VERSION="unknown"
PYTHON_VERSION="unknown"
PLATFORM="unknown"
GOMAXPROCS="8"

# Try to read Go benchmark header for platform info
if [ -f "${RESULTS_DIR}/bench-go.txt" ]; then
    PLATFORM=$(awk '/^cpu:/ { $1=""; print substr($0,2) }' "${RESULTS_DIR}/bench-go.txt" | head -1)
    GOARCH=$(awk '/^goarch:/ { print $2 }' "${RESULTS_DIR}/bench-go.txt" | head -1)
    GOOS=$(awk '/^goos:/ { print $2 }' "${RESULTS_DIR}/bench-go.txt" | head -1)
    [ -n "$GOARCH" ] && [ -n "$GOOS" ] && PLATFORM="${PLATFORM:+$PLATFORM, }${GOOS}/${GOARCH}"
fi

# Try to read meta.json for version info
META_JSON="${PROJECT_DIR}/testdata/benchmarks/v0.4.0/meta.json"
if [ -f "$META_JSON" ]; then
    GOBFD_VERSION=$(python3 -c "import json; print(json.load(open('${META_JSON}'))['version'])" 2>/dev/null || echo "unknown")
    GO_VERSION=$(python3 -c "import json; print(json.load(open('${META_JSON}'))['go'])" 2>/dev/null || echo "unknown")
fi

# GCC version from C benchmark stderr (or from container)
GCC_VERSION="gcc (Alpine)"
PYTHON_VERSION="Python 3.12"

# -----------------------------------------------------------------------
# Build JSON data structure
# -----------------------------------------------------------------------

# Collect all benchmark results and export for Python
export GO_DATA=$(parse_go)
export FRR_DATA=$(parse_bench "${RESULTS_DIR}/bench-c-frr.txt")
export BIRD_DATA=$(parse_bench "${RESULTS_DIR}/bench-c-bird.txt")
export AIOBFD_DATA=$(parse_bench "${RESULTS_DIR}/bench-python-aiobfd.txt")
export STRUCT_DATA=$(parse_bench "${RESULTS_DIR}/bench-python-struct.txt")

# Export metadata for Python
export GENERATED GOBFD_VERSION GO_VERSION GCC_VERSION PYTHON_VERSION PLATFORM GOMAXPROCS

# Build the complete JSON using Python (robust JSON construction)
JSON=$(python3 -c "
import json, sys, re
from collections import defaultdict

# Read tagged benchmark lines from stdin
go_data = []    # list of {name, ns_op, b_op, allocs_op}
frr_data = {}   # name -> ns_op (already averaged by parse_bench)
bird_data = {}
aiobfd_data = {}
pystruct_data = {}

def parse_json_lines(text):
    results = []
    for line in text.strip().split('\n'):
        line = line.strip()
        if not line:
            continue
        try:
            results.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    return results

# Read from environment (passed as shell variables)
import os
go_raw = os.environ.get('GO_DATA', '')
frr_raw = os.environ.get('FRR_DATA', '')
bird_raw = os.environ.get('BIRD_DATA', '')
aiobfd_raw = os.environ.get('AIOBFD_DATA', '')
struct_raw = os.environ.get('STRUCT_DATA', '')

# Parse Go data (multiple runs per benchmark — need to average)
go_entries = parse_json_lines(go_raw)
go_sum = defaultdict(lambda: {'ns_sum': 0.0, 'count': 0, 'b_op': 0, 'allocs_op': 0})
for e in go_entries:
    name = e['name']
    go_sum[name]['ns_sum'] += float(e['ns_op'])
    go_sum[name]['count'] += 1
    if 'b_op' in e:
        go_sum[name]['b_op'] = int(e['b_op'])
    if 'allocs_op' in e:
        go_sum[name]['allocs_op'] = int(e['allocs_op'])
go_avg = {}
for name, v in go_sum.items():
    go_avg[name] = {
        'ns_op': round(v['ns_sum'] / v['count'], 3),
        'b_op': v['b_op'],
        'allocs_op': v['allocs_op'],
    }

# Parse C/Python data (already averaged by parse_bench)
for raw, target in [(frr_raw, frr_data), (bird_raw, bird_data),
                     (aiobfd_raw, aiobfd_data), (struct_raw, pystruct_data)]:
    for e in parse_json_lines(raw):
        target[e['name']] = e['ns_op']

# Go name -> canonical name mapping
canon = {
    'ControlPacketMarshal': 'Marshal',
    'ControlPacketMarshalWithAuth': 'MarshalWithAuth',
    'ControlPacketUnmarshal': 'Unmarshal',
    'ControlPacketUnmarshalWithAuth': 'UnmarshalWithAuth',
    'ControlPacketRoundTrip': 'RoundTrip',
    'FSMTransitionUpRecvUp': 'FSMUpRecvUp',
    'FSMTransitionDownRecvDown': 'FSMDownRecvDown',
    'FSMTransitionUpTimerExpired': 'FSMUpTimerExpired',
    'FSMTransitionIgnored': 'FSMIgnored',
    'ApplyJitter': 'Jitter',
    'ApplyJitterDetectMultOne': 'JitterDetectMultOne',
    'PacketPool': 'PacketPool',
    'RecvStateToEvent': 'RecvStateToEvent',
    'FullRecvPath': 'FullRxPath',
    'FullTxPath': 'FullTxPath',
    'SessionRecvPacket': 'SessionRecvPacket',
    'DetectionTimeCalc': 'DetectionTimeCalc',
    'CalcTxInterval': 'CalcTxInterval',
    'ManagerCreate100Sessions': 'ManagerCreate100Sessions',
    'ManagerCreate1000Sessions': 'ManagerCreate1000Sessions',
    'ManagerDemux1000Sessions': 'ManagerDemux1000Sessions',
    'ManagerReconcile': 'ManagerReconcile',
    'SessionApplyJitter': 'SessionApplyJitter',
    'DetectionTimeCalcHot': 'DetectionTimeCalcHot',
    'CalcTxIntervalHot': 'CalcTxIntervalHot',
    'FullRecvPathCodec': 'FullRxPathCodec',
    'ManagerLookup1000Sessions': 'SessionLookup1000',
}

# Reverse map: canonical -> go_original
canon_rev = {}
for orig, c in canon.items():
    if orig in go_avg:
        canon_rev[c] = orig

# Collect all canonical benchmark names (preserving order)
seen = set()
ordered_names = []
# Go benchmarks first (in order they appear)
for e in go_entries:
    c = canon.get(e['name'], e['name'])
    if c not in seen:
        seen.add(c)
        ordered_names.append(c)
# Then C/Python benchmarks
for d in [frr_data, bird_data, aiobfd_data, pystruct_data]:
    for name in d:
        if name not in seen:
            seen.add(name)
            ordered_names.append(name)

# Build benchmarks array
benchmarks = []
for cname in ordered_names:
    entry = {'name': cname}
    # Go data
    go_orig = canon_rev.get(cname, cname)
    if go_orig in go_avg:
        g = go_avg[go_orig]
        entry['go'] = {'ns_op': g['ns_op'], 'b_op': g['b_op'], 'allocs_op': g['allocs_op']}
    # C/Python data
    if cname in frr_data:
        entry['frr'] = {'ns_op': frr_data[cname]}
    if cname in bird_data:
        entry['bird'] = {'ns_op': bird_data[cname]}
    if cname in aiobfd_data:
        entry['aiobfd'] = {'ns_op': aiobfd_data[cname]}
    if cname in pystruct_data:
        entry['pystruct'] = {'ns_op': pystruct_data[cname]}
    benchmarks.append(entry)

# Get headline values (Marshal ns/op for each impl)
go_marshal = go_avg.get('ControlPacketMarshal', {}).get('ns_op', 0)
frr_marshal = frr_data.get('Marshal', 0)
bird_marshal = bird_data.get('Marshal', 0)
aiobfd_marshal = aiobfd_data.get('Marshal', 0)
pystruct_marshal = pystruct_data.get('Marshal', 0)

# Build final JSON
meta = {
    'generated': os.environ.get('GENERATED', ''),
    'gobfd_version': os.environ.get('GOBFD_VERSION', ''),
    'go_version': os.environ.get('GO_VERSION', ''),
    'gcc_version': os.environ.get('GCC_VERSION', ''),
    'python_version': os.environ.get('PYTHON_VERSION', ''),
    'platform': os.environ.get('PLATFORM', ''),
    'gomaxprocs': os.environ.get('GOMAXPROCS', '8'),
}

implementations = [
    {'id': 'go', 'name': 'GoBFD (Go)', 'short_name': 'Go', 'color': '#00d4ff',
     'has_allocs': True, 'headline_value': f'{go_marshal:.1f}',
     'headline_unit': 'ns/op', 'headline_label': 'Packet Marshal'},
    {'id': 'frr', 'name': 'C (FRR bfdd)', 'short_name': 'C-FRR', 'color': '#4ade80',
     'has_allocs': False, 'headline_value': f'{frr_marshal:.1f}',
     'headline_unit': 'ns/op', 'headline_label': 'Marshal (stack alloc)'},
    {'id': 'bird', 'name': 'C (BIRD BFD)', 'short_name': 'C-BIRD', 'color': '#fbbf24',
     'has_allocs': False, 'headline_value': f'{bird_marshal:.1f}',
     'headline_unit': 'ns/op', 'headline_label': 'Marshal (pre-alloc)'},
    {'id': 'aiobfd', 'name': 'Python (aiobfd)', 'short_name': 'Py-aiobfd', 'color': '#f472b6',
     'has_allocs': False, 'headline_value': f'{aiobfd_marshal:.0f}',
     'headline_unit': 'ns/op', 'headline_label': 'Marshal (bitstring)'},
    {'id': 'pystruct', 'name': 'Python (struct)', 'short_name': 'Py-struct', 'color': '#a78bfa',
     'has_allocs': False, 'headline_value': f'{pystruct_marshal:.0f}',
     'headline_unit': 'ns/op', 'headline_label': 'Marshal (struct.pack)'},
]

features = [
    {'name': 'RFC 5880 (BFD Base)', 'go': True, 'frr': True, 'bird': True, 'aiobfd': 'partial'},
    {'name': 'RFC 5881 (IPv4/IPv6)', 'go': True, 'frr': True, 'bird': True, 'aiobfd': True},
    {'name': 'Keyed SHA1 Auth', 'go': True, 'frr': True, 'bird': True, 'aiobfd': False},
    {'name': 'Keyed MD5 Auth', 'go': True, 'frr': True, 'bird': True, 'aiobfd': False},
    {'name': 'VXLAN BFD (RFC 8971)', 'go': True, 'frr': False, 'bird': False, 'aiobfd': False},
    {'name': 'Geneve BFD (RFC 9521)', 'go': True, 'frr': False, 'bird': False, 'aiobfd': False},
    {'name': 'Multihop (RFC 5883)', 'go': True, 'frr': True, 'bird': True, 'aiobfd': False},
    {'name': 'Micro-BFD (RFC 7130)', 'go': True, 'frr': False, 'bird': False, 'aiobfd': False},
    {'name': 'CPI Bit', 'go': True, 'frr': 'partial', 'bird': False, 'aiobfd': False},
    {'name': 'Zero-alloc Hot Path', 'go': True, 'frr': 'n/a', 'bird': 'n/a', 'aiobfd': False},
    {'name': 'Goroutine-per-Session', 'go': True, 'frr': False, 'bird': False, 'aiobfd': False},
    {'name': 'gRPC / ConnectRPC API', 'go': True, 'frr': False, 'bird': False, 'aiobfd': False},
    {'name': 'Prometheus Metrics', 'go': True, 'frr': False, 'bird': False, 'aiobfd': False},
]

# Annotations for benchmarks: explanations for Go-only benchmarks
# and notes about performance differences
annotations = {
    'MarshalWithAuth': {'go_only': 'Requires crypto (SHA1 HMAC) not in bench container'},
    'UnmarshalWithAuth': {'go_only': 'Requires crypto (SHA1 HMAC) not in bench container'},
    'PacketPool': {'go_only': 'sync.Pool is Go GC-specific; C has no GC, Python has no explicit pool'},
    'SessionRecvPacket': {'go_only': 'Measures goroutine channel send; C uses poll(), Python uses asyncio'},
    'SessionApplyJitter': {'go_only': 'Session-local PCG PRNG optimization; unique to Go goroutine model'},
    'JitterDetectMultOne': {'go_only': 'DetectMult=1 variant of global jitter'},
    'ManagerReconcile': {'go_only': 'Config reconciliation pattern unique to Go Manager'},
    'ManagerCreate100Sessions': {'go_only': 'Go Manager with goroutine-per-session; not comparable to C/Python'},
    'ManagerCreate1000Sessions': {'go_only': 'Go Manager with goroutine-per-session; not comparable to C/Python'},
    'ManagerDemux1000Sessions': {'go_only': 'Go Manager discriminator map + channel send'},
    'DetectionTimeCalcHot': {'go_only': 'Uses cachedState (goroutine-confined); no atomic load'},
    'CalcTxIntervalHot': {'go_only': 'Uses cachedState (goroutine-confined); no atomic load'},
    'Unmarshal': {'note': 'Go: 7 RFC 5880 \\u00a76.8.6 validation checks; C: 3 checks'},
    'DetectionTimeCalc': {'note': 'Go uses atomic State() load; hot path uses cachedState'},
    'CalcTxInterval': {'note': 'Go uses atomic State() load; hot path uses cachedState'},
    'Jitter': {'note': 'Go global PRNG (atomic); session-local PRNG avoids contention'},
    'FullRxPath': {'note': 'UNFAIR: Go = unmarshal + RWMutex + map + channel send; C = unmarshal + FSM (no IPC). See FullRxPathCodec for fair comparison'},
    'FullRxPathCodec': {'go_only': 'Codec-only RX path (unmarshal + FSM); fair comparison with C FullRxPath'},
    'SessionDemux1000': {'note': 'UNFAIR: Go = RWMutex + map + channel send; C = hashmap lookup only. See SessionLookup1000 for fair comparison'},
    'SessionCreate1000': {'note': 'DIFFERENT OPS: Go creates goroutines + channels + context; C allocates structs + hashmap insert'},
    'SessionLookup1000': {'go_only': 'Pure RWMutex + map lookup (no channel send); fair comparison with C SessionDemux1000'},
}

# Add annotations to benchmark entries
for b in benchmarks:
    if b['name'] in annotations:
        b['annotation'] = annotations[b['name']]

result = {
    'meta': meta,
    'implementations': implementations,
    'benchmarks': benchmarks,
    'features': features,
}

print(json.dumps(result, separators=(',', ':')))
")

# -----------------------------------------------------------------------
# Inject JSON into template
# -----------------------------------------------------------------------
# Use a Python one-liner for safe JSON injection (avoids sed issues with special chars)
python3 -c "
import sys
template = open('${TEMPLATE}').read()
data = sys.stdin.read()
result = template.replace('__BENCHMARK_DATA__', data)
open('${OUTPUT}', 'w').write(result)
" <<< "$JSON"

echo "=== Cross-comparison report: ${OUTPUT} ==="
echo "=== Open in browser to view interactive results ==="

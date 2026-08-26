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

go -C "${PROJECT_DIR}" run ./test/cmd/benchreport -- \
    "${GO_INPUT}" "${FRR_INPUT}" "${BIRD_INPUT}" \
    "${META_JSON}" "${TEMPLATE}" "${OUTPUT}"

printf '=== Cross-comparison report: %s ===\n' "${OUTPUT}"
printf '%s\n' '=== Open in browser to view interactive results ==='

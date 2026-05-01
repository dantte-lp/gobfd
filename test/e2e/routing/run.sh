#!/usr/bin/env bash
# S10.3 routing E2E runner. Go assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/routing/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
DEV_COMPOSE="${ROOT_DIR}/deployments/compose/compose.dev.yml"
TSHARK_IMAGE="localhost/interop_tshark:latest"

mkdir -p "${REPORT_DIR}/interop" "${REPORT_DIR}/interop-bgp"
: >"${REPORT_DIR}/go-test.json"
: >"${REPORT_DIR}/go-test.log"
: >"${REPORT_DIR}/containers.log"

DEV_DC=(podman-compose -p "${DEV_PROJECT}" -f "${DEV_COMPOSE}")

write_environment() {
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-routing",
  "run_id": "${RUN_ID}",
  "dev_project": "${DEV_PROJECT}",
  "podman_runtime": "podman",
  "suites": ["interop", "interop-bgp"]
}
EOF_ENV
}

append_csv() {
    local suite="$1"
    local file="$2"

    if [ ! -s "${file}" ]; then
        return 0
    fi
    if [ ! -s "${REPORT_DIR}/packets.csv" ]; then
        sed '1s/^/suite,/' "${file}" | awk -v suite="${suite}" 'NR == 1 {print; next} {print suite "," $0}' \
            >"${REPORT_DIR}/packets.csv"
        return 0
    fi
    awk -v suite="${suite}" 'NR > 1 {print suite "," $0}' "${file}" >>"${REPORT_DIR}/packets.csv"
}

record_containers() {
    local suite="$1"
    shift
    local suite_dir="${REPORT_DIR}/${suite}"

    podman inspect "$@" >"${suite_dir}/containers.json" 2>"${suite_dir}/containers.err" || true
}

collect_pcap() {
    local suite="$1"
    local tshark_container="$2"
    local suite_dir="${REPORT_DIR}/${suite}"

    podman exec "${tshark_container}" cat /captures/bfd.pcapng >"${suite_dir}/packets.pcapng" 2>"${suite_dir}/packets.err" || true
    podman exec "${tshark_container}" tshark -r /captures/bfd.pcapng -Y bfd \
        -T fields \
        -e frame.time_relative \
        -e ip.src \
        -e ip.dst \
        -e udp.srcport \
        -e udp.dstport \
        -e bfd.sta \
        -e bfd.diag \
        -e bfd.my_discriminator \
        -e bfd.your_discriminator \
        -E header=y \
        -E separator=, >"${suite_dir}/packets.csv" 2>"${suite_dir}/packets-csv.err" || true
    append_csv "${suite}" "${suite_dir}/packets.csv"
}

merge_artifacts() {
    python3 - "${REPORT_DIR}" <<'PY'
import json
import pathlib
import sys

report = pathlib.Path(sys.argv[1])
suites = {}
for name in ("interop", "interop-bgp"):
    path = report / name / "containers.json"
    if path.exists() and path.stat().st_size:
        suites[name] = json.loads(path.read_text())
    else:
        suites[name] = []
(report / "containers.json").write_text(json.dumps({"suites": suites}, indent=2) + "\n")
PY

    if [ -s "${REPORT_DIR}/interop/packets.pcapng" ] && [ -s "${REPORT_DIR}/interop-bgp/packets.pcapng" ]; then
        podman run --rm --entrypoint /usr/bin/mergecap \
            -v "${REPORT_DIR}:/reports:z" "${TSHARK_IMAGE}" \
            -w /reports/packets.pcapng \
            /reports/interop/packets.pcapng \
            /reports/interop-bgp/packets.pcapng >/dev/null 2>"${REPORT_DIR}/mergecap.err" || true
    fi
}

write_summary() {
    local status="$1"
    cat >"${REPORT_DIR}/summary.md" <<EOF_SUMMARY
# e2e-routing Summary

| Field | Value |
|---|---|
| Target | \`make e2e-routing\` |
| Run ID | \`${RUN_ID}\` |
| Exit code | \`${status}\` |
| Go test JSON | \`go-test.json\` |
| Go test log | \`go-test.log\` |
| Container state | \`containers.json\` |
| Container logs | \`containers.log\` |
| Packet CSV | \`packets.csv\` |
| Merged packet capture | \`packets.pcapng\` |
| BFD interop artifacts | \`interop/\` |
| BGP+BFD interop artifacts | \`interop-bgp/\` |
EOF_SUMMARY
}

run_suite() {
    local suite="$1"
    local compose_file="$2"
    local env_name="$3"
    local tag="$4"
    local package="$5"
    local tshark_container="$6"
    shift 6
    local containers=("$@")
    local suite_dir="${REPORT_DIR}/${suite}"
    local dc=(podman-compose -f "${compose_file}")

    "${dc[@]}" build --no-cache
    "${dc[@]}" up -d
    sleep 15

    set +e
    "${DEV_DC[@]}" exec -T dev env \
        "${env_name}=/app/${compose_file#"${ROOT_DIR}/"}" \
        go test -tags "${tag}" -json -v -count=1 -timeout 300s "${package}" \
        | tee -a "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log" >"${suite_dir}/go-test.json"
    local test_status="${PIPESTATUS[0]}"
    set -e

    cp "${suite_dir}/go-test.json" "${suite_dir}/go-test.log"
    {
        printf '\n===== %s containers =====\n' "${suite}"
        "${dc[@]}" logs
    } >>"${REPORT_DIR}/containers.log" 2>&1 || true
    collect_pcap "${suite}" "${tshark_container}"
    record_containers "${suite}" "${containers[@]}"
    "${dc[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    return "${test_status}"
}

cleanup() {
    local status="$?"
    podman-compose -f "${ROOT_DIR}/test/interop/compose.yml" down --volumes --remove-orphans >/dev/null 2>&1 || true
    podman-compose -f "${ROOT_DIR}/test/interop-bgp/compose.yml" down --volumes --remove-orphans >/dev/null 2>&1 || true
    merge_artifacts
    write_environment
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

run_suite "interop" "${ROOT_DIR}/test/interop/compose.yml" "INTEROP_COMPOSE_FILE" "interop" "./test/interop/" "tshark-interop" \
    gobfd-interop frr-interop bird3-interop tshark-interop aiobfd-interop thoro-interop

run_suite "interop-bgp" "${ROOT_DIR}/test/interop-bgp/compose.yml" "INTEROP_BGP_COMPOSE_FILE" "interop_bgp" "./test/interop-bgp/" "tshark-bgp-interop" \
    gobfd-bgp-interop gobgp-interop frr-bgp-interop bird3-bgp-interop gobfd-exabgp-interop exabgp-interop tshark-bgp-interop

printf 'S10.3 routing E2E artifacts: %s\n' "${REPORT_DIR}"

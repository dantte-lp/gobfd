#!/usr/bin/env bash
# S10.4 RFC E2E runner. Go assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/rfc/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
COMPOSE_FILE="${ROOT_DIR}/test/interop-rfc/compose.yml"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
DEV_COMPOSE="${ROOT_DIR}/deployments/compose/compose.dev.yml"

mkdir -p "${REPORT_DIR}"
: >"${REPORT_DIR}/go-test.json"
: >"${REPORT_DIR}/go-test.log"

DC=(podman-compose -f "${COMPOSE_FILE}")
DEV_DC=(podman-compose -p "${DEV_PROJECT}" -f "${DEV_COMPOSE}")

collect_artifacts() {
    "${DC[@]}" logs >"${REPORT_DIR}/containers.log" 2>&1 || true
    podman inspect \
        gobfd-rfc-interop \
        tshark-rfc-interop \
        frr-rfc-interop \
        gobfd-rfc9384-interop \
        gobgp-rfc-interop \
        frr-rfc-bgp-interop \
        frr-rfc-unsolicited-interop \
        echo-reflector-interop \
        >"${REPORT_DIR}/containers.json" 2>"${REPORT_DIR}/containers.err" || true
    podman exec tshark-rfc-interop cat /captures/bfd.pcapng >"${REPORT_DIR}/packets.pcapng" 2>"${REPORT_DIR}/packets.err" || true
    podman exec tshark-rfc-interop tshark -r /captures/bfd.pcapng \
        -Y "bfd || udp.port == 3785" \
        -T fields \
        -e frame.time_relative \
        -e ip.src \
        -e ip.dst \
        -e udp.srcport \
        -e udp.dstport \
        -e bfd.sta \
        -e bfd.diag \
        -e bfd.desired_min_tx_interval \
        -e bfd.required_min_rx_interval \
        -e bfd.required_min_echo_interval \
        -e bfd.my_discriminator \
        -e bfd.your_discriminator \
        -E header=y \
        -E separator=, >"${REPORT_DIR}/packets.csv" 2>"${REPORT_DIR}/packets-csv.err" || true
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-rfc",
  "run_id": "${RUN_ID}",
  "dev_project": "${DEV_PROJECT}",
  "compose_file": "${COMPOSE_FILE}",
  "podman_runtime": "podman",
  "rfc_scenarios": ["RFC 7419", "RFC 9384", "RFC 9468", "RFC 9747"]
}
EOF_ENV
}

write_summary() {
    local status="$1"
    cat >"${REPORT_DIR}/summary.md" <<EOF_SUMMARY
# e2e-rfc Summary

| Field | Value |
|---|---|
| Target | \`make e2e-rfc\` |
| Run ID | \`${RUN_ID}\` |
| Exit code | \`${status}\` |
| RFC coverage | RFC 7419, RFC 9384, RFC 9468, RFC 9747 |
| Go test JSON | \`go-test.json\` |
| Go test log | \`go-test.log\` |
| Container state | \`containers.json\` |
| Container logs | \`containers.log\` |
| Packet capture | \`packets.pcapng\` |
| Packet CSV | \`packets.csv\` |
EOF_SUMMARY
}

cleanup() {
    local status="$?"
    collect_artifacts
    "${DC[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

"${DC[@]}" build --no-cache
"${DC[@]}" up -d
sleep 15

"${DEV_DC[@]}" exec -T dev env \
    INTEROP_RFC_COMPOSE_FILE=/app/test/interop-rfc/compose.yml \
    go test -tags interop_rfc -json -v -count=1 -timeout 300s ./test/interop-rfc/ \
    | tee "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log"

printf 'S10.4 RFC E2E artifacts: %s\n' "${REPORT_DIR}"

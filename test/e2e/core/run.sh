#!/usr/bin/env bash
# S10.2 core E2E runner. All Go assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/compose.yml"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/core/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
RUNTIME_DIR="${REPORT_DIR}/runtime"
CAPTURE_DIR="${REPORT_DIR}/captures"
PROJECT_SLUG="$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-${PROJECT_SLUG}}"
E2E_PROJECT="${E2E_CORE_PROJECT:-${DEV_PROJECT}-e2e-core}"
PORT_BASE="${E2E_CORE_PORT_BASE:-$((20000 + ($$ % 20000)))}"

mkdir -p "${RUNTIME_DIR}" "${CAPTURE_DIR}"

write_config() {
    local file="$1"
    local local_ip="$2"
    local peer_ip="$3"
    local log_level="$4"

    cat >"${file}" <<EOF_CONFIG
grpc:
  addr: ":50051"

metrics:
  addr: ":9100"
  path: "/metrics"

log:
  level: "${log_level}"
  format: "text"

bfd:
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3

sessions:
  - peer: "${peer_ip}"
    local: "${local_ip}"
    type: single_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
    auth:
      type: simple_password
      key_id: 7
      secret: "s10-core-auth"
EOF_CONFIG
}

write_config "${RUNTIME_DIR}/gobfd-a.yml" "172.30.10.10" "172.30.10.20" "debug"
write_config "${RUNTIME_DIR}/gobfd-b.yml" "172.30.10.20" "172.30.10.10" "debug"

export E2E_CORE_A_CONFIG="${RUNTIME_DIR}/gobfd-a.yml"
export E2E_CORE_B_CONFIG="${RUNTIME_DIR}/gobfd-b.yml"
export E2E_CORE_CAPTURE_DIR="${CAPTURE_DIR}"
export E2E_CORE_A_GRPC_PORT="${E2E_CORE_A_GRPC_PORT:-${PORT_BASE}}"
export E2E_CORE_A_METRICS_PORT="${E2E_CORE_A_METRICS_PORT:-$((PORT_BASE + 1))}"
export E2E_CORE_B_GRPC_PORT="${E2E_CORE_B_GRPC_PORT:-$((PORT_BASE + 2))}"
export E2E_CORE_B_METRICS_PORT="${E2E_CORE_B_METRICS_PORT:-$((PORT_BASE + 3))}"

DC=(podman-compose -p "${E2E_PROJECT}" -f "${COMPOSE_FILE}")
DEV_DC=(podman-compose -p "${DEV_PROJECT}" -f "${ROOT_DIR}/deployments/compose/compose.dev.yml")

cleanup() {
    local status="$?"
    "${DC[@]}" logs >"${REPORT_DIR}/containers.log" 2>&1 || true
    podman ps -a \
        --filter "label=io.podman.compose.project=${E2E_PROJECT}" \
        --format json >"${REPORT_DIR}/containers.json" 2>"${REPORT_DIR}/containers.err" || true
    podman-compose --profile tools -p "${E2E_PROJECT}" -f "${COMPOSE_FILE}" run --rm --no-deps analyzer \
        -r /captures/bfd.pcapng \
        -Y bfd \
        -T fields \
        -e frame.time_relative \
        -e ip.src \
        -e ip.dst \
        -e bfd.sta \
        -e bfd.diag \
        -e bfd.my_discriminator \
        -e bfd.your_discriminator \
        -E header=y \
        -E separator='	' >"${REPORT_DIR}/pcap-summary.tsv" 2>"${REPORT_DIR}/pcap-summary.err" || true
    podman-compose --profile tools -p "${E2E_PROJECT}" -f "${COMPOSE_FILE}" run --rm --no-deps analyzer \
        -r /captures/bfd.pcapng \
        -Y bfd \
        -T fields \
        -e frame.time_relative \
        -e ip.src \
        -e ip.dst \
        -e udp.srcport \
        -e udp.dstport \
        -e bfd.sta \
        -e bfd.diag \
        -e bfd.message_length \
        -e bfd.flags.a \
        -e bfd.auth.type \
        -e bfd.auth.key \
        -e bfd.my_discriminator \
        -e bfd.your_discriminator \
        -E header=y \
        -E separator=, >"${REPORT_DIR}/packets.csv" 2>"${REPORT_DIR}/packets.err" || true
    cp "${CAPTURE_DIR}/bfd.pcapng" "${REPORT_DIR}/packets.pcapng" 2>/dev/null || true
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-core",
  "run_id": "${RUN_ID}",
  "compose_project": "${E2E_PROJECT}",
  "compose_file": "${COMPOSE_FILE}",
  "dev_project": "${DEV_PROJECT}",
  "podman_runtime": "podman",
  "a_grpc_port": ${E2E_CORE_A_GRPC_PORT},
  "a_metrics_port": ${E2E_CORE_A_METRICS_PORT},
  "b_grpc_port": ${E2E_CORE_B_GRPC_PORT},
  "b_metrics_port": ${E2E_CORE_B_METRICS_PORT}
}
EOF_ENV
    cat >"${REPORT_DIR}/summary.md" <<EOF_SUMMARY
# e2e-core Summary

| Field | Value |
|---|---|
| Target | \`make e2e-core\` |
| Run ID | \`${RUN_ID}\` |
| Compose project | \`${E2E_PROJECT}\` |
| Exit code | \`${status}\` |
| Go test JSON | \`go-test.json\` |
| Packet capture | \`packets.pcapng\` |
| Packet CSV | \`packets.csv\` |
| Container logs | \`containers.log\` |
EOF_SUMMARY
    "${DC[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
    exit "${status}"
}

trap cleanup EXIT

"${DC[@]}" up --build -d

"${DEV_DC[@]}" exec -T dev go build -o /tmp/gobfdctl-e2e ./cmd/gobfdctl

"${DEV_DC[@]}" exec -T dev env \
    E2E_CORE_COMPOSE_FILE=/app/test/e2e/core/compose.yml \
    E2E_CORE_PROJECT="${E2E_PROJECT}" \
    E2E_CORE_A_CONFIG="${E2E_CORE_A_CONFIG}" \
    E2E_CORE_B_CONFIG="${E2E_CORE_B_CONFIG}" \
    E2E_CORE_A_GRPC_PORT="${E2E_CORE_A_GRPC_PORT}" \
    E2E_CORE_A_METRICS_PORT="${E2E_CORE_A_METRICS_PORT}" \
    E2E_CORE_B_GRPC_PORT="${E2E_CORE_B_GRPC_PORT}" \
    E2E_CORE_B_METRICS_PORT="${E2E_CORE_B_METRICS_PORT}" \
    E2E_CORE_A_CONFIG_IN_CONTAINER="/app/${REPORT_REL}/runtime/gobfd-a.yml" \
    E2E_CORE_REPORT_DIR="/app/${REPORT_REL}" \
    E2E_CORE_CAPTURE_DIR="${CAPTURE_DIR}" \
    E2E_CORE_GOBFDCTL=/tmp/gobfdctl-e2e \
    go test -tags e2e_core -json -v -count=1 -timeout 300s ./test/e2e/core/ \
    | tee "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log"

printf 'S10.2 core E2E artifacts: %s\n' "${REPORT_DIR}"

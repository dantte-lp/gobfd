#!/usr/bin/env bash
# S10.4 overlay E2E runner. Assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/overlay/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
DEV_COMPOSE="${ROOT_DIR}/deployments/compose/compose.dev.yml"

mkdir -p "${REPORT_DIR}"

DEV_DC=(podman-compose -p "${DEV_PROJECT}" -f "${DEV_COMPOSE}")

write_environment() {
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-overlay",
  "run_id": "${RUN_ID}",
  "dev_project": "${DEV_PROJECT}",
  "podman_runtime": "podman",
  "reserved_backends": ["kernel", "ovs", "ovn", "cilium", "calico", "nsx"]
}
EOF_ENV
}

write_summary() {
    local status="$1"
    cat >"${REPORT_DIR}/summary.md" <<EOF_SUMMARY
# e2e-overlay Summary

| Field | Value |
|---|---|
| Target | \`make e2e-overlay\` |
| Run ID | \`${RUN_ID}\` |
| Exit code | \`${status}\` |
| Go test JSON | \`go-test.json\` |
| Go test log | \`go-test.log\` |
| Container state | \`containers.json\` |
| Container logs | \`containers.log\` |
| Packet evidence | \`packets.csv\` |
EOF_SUMMARY
}

cleanup() {
    local status="$?"
    podman ps -a --filter "label=io.podman.compose.project=${DEV_PROJECT}" --format json \
        >"${REPORT_DIR}/containers.json" 2>"${REPORT_DIR}/containers.err" || true
    "${DEV_DC[@]}" logs >"${REPORT_DIR}/containers.log" 2>&1 || true
    write_environment
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

"${DEV_DC[@]}" exec -T dev env \
    E2E_OVERLAY_PACKET_CSV="/app/${REPORT_REL}/packets.csv" \
    go test -tags e2e_overlay -json -v -count=1 ./test/e2e/overlay/ \
    | tee "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log"

printf 'S10.4 overlay E2E artifacts: %s\n' "${REPORT_DIR}"

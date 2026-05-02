#!/usr/bin/env bash
# S10.5 Linux dataplane E2E runner.
#
# The test binary is compiled in the dev container, then executed in a
# disposable Podman container with --network none. That gives the test its own
# network namespace for veth/rtnetlink mutations while keeping host interfaces
# untouched.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/linux/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
DEV_COMPOSE="${ROOT_DIR}/deployments/compose/compose.dev.yml"
TEST_IMAGE="${E2E_LINUX_IMAGE:-localhost/${DEV_PROJECT}_dev:latest}"
TEST_BINARY="${REPORT_DIR}/e2e-linux.test"

mkdir -p "${REPORT_DIR}"
: >"${REPORT_DIR}/go-test.json"
: >"${REPORT_DIR}/go-test.log"

DEV_DC=(podman-compose -p "${DEV_PROJECT}" -f "${DEV_COMPOSE}")

write_environment() {
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-linux",
  "run_id": "${RUN_ID}",
  "dev_project": "${DEV_PROJECT}",
  "test_image": "${TEST_IMAGE}",
  "podman_runtime": "podman",
  "isolation": "podman --network none --cap-add NET_ADMIN --cap-add NET_RAW"
}
EOF_ENV
}

write_summary() {
    local status="$1"
    cat >"${REPORT_DIR}/summary.md" <<EOF_SUMMARY
# e2e-linux Summary

| Field | Value |
|---|---|
| Target | \`make e2e-linux\` |
| Run ID | \`${RUN_ID}\` |
| Exit code | \`${status}\` |
| Isolation | \`podman --network none\` |
| Go test JSON | \`go-test.json\` |
| Go test log | \`go-test.log\` |
| Container state | \`containers.json\` |
| Container logs | \`containers.log\` |
| Linux events | \`link-events.json\` |
| LAG backend evidence | \`lag-backends.json\` |
EOF_SUMMARY
}

cleanup() {
    local status="$?"
    podman ps -a --filter "name=e2e-linux-${RUN_ID}" --format json \
        >"${REPORT_DIR}/containers.json" 2>"${REPORT_DIR}/containers.err" || true
    podman logs "e2e-linux-${RUN_ID}" >"${REPORT_DIR}/containers.log" 2>"${REPORT_DIR}/containers-log.err" || true
    podman rm -f "e2e-linux-${RUN_ID}" >/dev/null 2>&1 || true
    write_environment
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

"${DEV_DC[@]}" exec -T dev env CGO_ENABLED=0 \
    go test -tags e2e_linux -c -o "/app/${REPORT_REL}/e2e-linux.test" ./test/e2e/linux/

podman create \
    --name "e2e-linux-${RUN_ID}" \
    --network none \
    --cap-drop ALL \
    --cap-add NET_ADMIN \
    --cap-add NET_RAW \
    --security-opt label=disable \
    -e E2E_LINUX_REPORT_DIR=/report \
    -v "${REPORT_DIR}:/report:z" \
    "${TEST_IMAGE}" \
    sh -c 'cd /report && go tool test2json -t ./e2e-linux.test -test.v -test.timeout=120s' >/dev/null

set +e
podman start -a "e2e-linux-${RUN_ID}" \
    | tee "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log"
test_status="${PIPESTATUS[0]}"
set -e

if [ "${test_status}" -ne 0 ]; then
    exit "${test_status}"
fi

printf 'S10.5 Linux E2E artifacts: %s\n' "${REPORT_DIR}"

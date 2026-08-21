#!/usr/bin/env bash
# S10.3 routing E2E runner. Go assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/routing/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
INTEROP_PROJECT_NAME="${INTEROP_PROJECT_NAME:-gobfd-interop}"
INTEROP_BGP_PROJECT_NAME="${INTEROP_BGP_PROJECT_NAME:-${INTEROP_PROJECT_NAME}-bgp}"
DEV_COMPOSE="${ROOT_DIR}/deployments/compose/compose.dev.yml"
TSHARK_IMAGE="localhost/interop_tshark:latest"

for project_name in "${DEV_PROJECT}" "${INTEROP_PROJECT_NAME}" "${INTEROP_BGP_PROJECT_NAME}"; do
    if [[ ! "${project_name}" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
        printf 'invalid Compose project name %q: use lowercase letters, digits, dashes, and underscores\n' \
            "${project_name}" >&2
        exit 2
    fi
done

mkdir -p "${REPORT_DIR}/interop" "${REPORT_DIR}/interop-bgp"
: >"${REPORT_DIR}/go-test.json"
: >"${REPORT_DIR}/go-test.log"
: >"${REPORT_DIR}/containers.log"

DEV_DC=(timeout 7m podman-compose -p "${DEV_PROJECT}" -f "${DEV_COMPOSE}")
PODMAN=(timeout 2m podman)
OWNED_PROJECTS=()

write_environment() {
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-routing",
  "run_id": "${RUN_ID}",
  "dev_project": "${DEV_PROJECT}",
  "interop_project": "${INTEROP_PROJECT_NAME}",
  "interop_bgp_project": "${INTEROP_BGP_PROJECT_NAME}",
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

    "${PODMAN[@]}" inspect "$@" >"${suite_dir}/containers.json" 2>"${suite_dir}/containers.err" || true
}

collect_pcap() {
    local suite="$1"
    local tshark_container="$2"
    local suite_dir="${REPORT_DIR}/${suite}"

    "${PODMAN[@]}" exec "${tshark_container}" cat /captures/bfd.pcapng >"${suite_dir}/packets.pcapng" 2>"${suite_dir}/packets.err" || true
    "${PODMAN[@]}" exec "${tshark_container}" tshark -r /captures/bfd.pcapng -Y bfd \
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
    local merge_status=0
    python3 - "${REPORT_DIR}" <<'PY' || merge_status=1
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
        "${PODMAN[@]}" run --rm \
            --label "com.docker.compose.project=${INTEROP_PROJECT_NAME}" \
            --entrypoint /usr/bin/mergecap \
            -v "${REPORT_DIR}:/reports:z" "${TSHARK_IMAGE}" \
            -w /reports/packets.pcapng \
            /reports/interop/packets.pcapng \
            /reports/interop-bgp/packets.pcapng >/dev/null 2>"${REPORT_DIR}/mergecap.err" || true
    fi
    return "${merge_status}"
}

query_project_resources() {
    local project_name="$1"
    local project_label="com.docker.compose.project=${project_name}"
    local containers networks volumes
    containers="$(timeout 30s podman ps -a --no-trunc --filter "label=${project_label}" --format '{{.ID}}')" || return 1
    networks="$(timeout 30s podman network ls --no-trunc --filter "label=${project_label}" --format '{{.ID}}')" || return 1
    volumes="$(timeout 30s podman volume ls --filter "label=${project_label}" --format '{{.Name}}')" || return 1

    local id
    while IFS= read -r id; do
        [ -n "${id}" ] && printf 'container:%s\n' "${id}"
    done <<<"${containers}"
    while IFS= read -r id; do
        [ -n "${id}" ] && printf 'network:%s\n' "${id}"
    done <<<"${networks}"
    while IFS= read -r id; do
        [ -n "${id}" ] && printf 'volume:%s\n' "${id}"
    done <<<"${volumes}"
    return 0
}

assert_project_available() {
    local project_name="$1"
    local resources
    if ! resources="$(query_project_resources "${project_name}")"; then
        printf 'failed to query exact resources for Compose project %s\n' "${project_name}" >&2
        return 1
    fi
    if [ -n "${resources}" ]; then
        printf 'Compose project %s already owns resources; refusing collision\n%s\n' \
            "${project_name}" "${resources}" >&2
        return 1
    fi
    return 0
}

remove_project_resources() {
    local project_name="$1"
    local resources
    resources="$(query_project_resources "${project_name}")" || return 1

    local kind id
    while IFS=: read -r kind id; do
        [ -z "${kind}" ] && continue
        case "${kind}" in
            container)
                if ! timeout 30s podman rm -f -- "${id}" >/dev/null; then
                    printf 'exactly labelled container %s could not be removed\n' "${id}" >&2
                fi
                ;;
            network)
                if ! timeout 30s podman network rm -- "${id}" >/dev/null; then
                    printf 'exactly labelled network %s could not be removed\n' "${id}" >&2
                fi
                ;;
            volume)
                if ! timeout 30s podman volume rm -- "${id}" >/dev/null; then
                    printf 'exactly labelled volume %s could not be removed\n' "${id}" >&2
                fi
                ;;
        esac
    done <<<"${resources}"
    return 0
}

verify_project_absent() {
    local project_name="$1"
    local resources
    resources="$(query_project_resources "${project_name}")" || return 1
    if [ -n "${resources}" ]; then
        printf 'owned-resource leak for Compose project %s:\n%s\n' "${project_name}" "${resources}" >&2
        return 1
    fi
    return 0
}

cleanup_project() {
    local project_name="$1"
    local compose_file="$2"
    timeout 2m podman-compose -p "${project_name}" -f "${compose_file}" \
        down --volumes --remove-orphans >/dev/null 2>&1 || \
        printf 'Compose cleanup was partial for project %s; resolving exact labelled resources\n' \
            "${project_name}" >&2
    remove_project_resources "${project_name}" || return 1
    verify_project_absent "${project_name}"
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
    local project_name="$7"
    shift 7
    local containers=("$@")
    local suite_dir="${REPORT_DIR}/${suite}"
    local dc=(podman-compose -p "${project_name}" -f "${compose_file}")

    assert_project_available "${project_name}"
    OWNED_PROJECTS+=("${project_name}:${compose_file}")
    timeout 10m "${dc[@]}" build --no-cache
    timeout 2m "${dc[@]}" up -d
    sleep 15

    set +e
    "${DEV_DC[@]}" exec -T dev env \
        "INTEROP_PROJECT_NAME=${project_name}" \
        "${env_name}=/app/${compose_file#"${ROOT_DIR}/"}" \
        go test -tags "${tag}" -json -v -count=1 -timeout 300s "${package}" \
        | tee -a "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log" >"${suite_dir}/go-test.json"
    local test_status="${PIPESTATUS[0]}"
    set -e

    if ! jq -s -e '[.[] | select(.Action == "pass" and has("Test"))] | length > 0' \
        "${suite_dir}/go-test.json" >/dev/null; then
        printf 'suite %s produced no passed Go tests\n' "${suite}" >&2
        test_status=1
    fi

    cp "${suite_dir}/go-test.json" "${suite_dir}/go-test.log"
    {
        printf '\n===== %s containers =====\n' "${suite}"
        timeout 2m "${dc[@]}" logs
    } >>"${REPORT_DIR}/containers.log" 2>&1 || true
    collect_pcap "${suite}" "${tshark_container}"
    record_containers "${suite}" "${containers[@]}"
    cleanup_project "${project_name}" "${compose_file}" || test_status=1
    return "${test_status}"
}

cleanup() {
    local status="$?"
    trap - EXIT
    local owned project_name compose_file
    if ! merge_artifacts; then
        printf 'routing artifact merge failed; preserving suite and cleanup status\n' >&2
    fi
    for owned in "${OWNED_PROJECTS[@]}"; do
        project_name="${owned%%:*}"
        compose_file="${owned#*:}"
        cleanup_project "${project_name}" "${compose_file}" || status=1
    done
    write_environment
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

run_suite "interop" "${ROOT_DIR}/test/interop/compose.yml" "INTEROP_COMPOSE_FILE" "interop" "./test/interop/" "tshark-interop" \
    "${INTEROP_PROJECT_NAME}" gobfd-interop frr-interop bird3-interop tshark-interop holo-interop holo-config-interop thoro-interop

run_suite "interop-bgp" "${ROOT_DIR}/test/interop-bgp/compose.yml" "INTEROP_BGP_COMPOSE_FILE" "interop_bgp" "./test/interop-bgp/" "tshark-bgp-interop" \
    "${INTEROP_BGP_PROJECT_NAME}" gobfd-bgp-interop gobgp-interop frr-bgp-interop bird3-bgp-interop gobfd-exabgp-interop exabgp-interop tshark-bgp-interop

printf 'S10.3 routing E2E artifacts: %s\n' "${REPORT_DIR}"

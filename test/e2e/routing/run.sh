#!/usr/bin/env bash
# S10.3 routing E2E runner. Go assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_TIMESTAMP="$(date -u +%Y%m%dT%H%M%S%NZ)"
RUN_ID="${RUN_TIMESTAMP}-$$"
REPORT_REL="reports/e2e/routing/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
INTEROP_PROJECT_NAME="${INTEROP_PROJECT_NAME:-gobfd-interop}"
INTEROP_BGP_PROJECT_NAME="${INTEROP_BGP_PROJECT_NAME:-${INTEROP_PROJECT_NAME}-bgp}"
MERGE_OWNER_LABEL_KEY="io.gobfd.e2e.merge-owner"
MERGE_OWNER_LABEL_VALUE="${RUN_ID}"
if [[ ! "${MERGE_OWNER_LABEL_VALUE}" =~ ^[0-9]{8}T[0-9]{15}Z-[0-9]+$ ]]; then
    printf 'invalid merge ownership label value %q\n' "${MERGE_OWNER_LABEL_VALUE}" >&2
    exit 2
fi

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

PODMAN=(timeout 2m podman)
# shellcheck source=test/interop/project_guard.sh
source "${ROOT_DIR}/test/interop/project_guard.sh"
OWNED_PROJECTS=()
declare -A PROJECT_LOCK_FDS=()

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
    local project_name="$2"
    shift 2
    local suite_dir="${REPORT_DIR}/${suite}"
    local container_name container_id
    local container_ids=()

    for container_name in "$@"; do
        if container_id="$(resolve_project_container_id "${project_name}" "${container_name}")"; then
            container_ids+=("${container_id}")
        fi
    done
    if [[ "${#container_ids[@]}" -eq 0 ]]; then
        printf '[]\n' >"${suite_dir}/containers.json"
        : >"${suite_dir}/containers.err"
        return 0
    fi
    "${PODMAN[@]}" inspect "${container_ids[@]}" >"${suite_dir}/containers.json" \
        2>"${suite_dir}/containers.err" || true
}

collect_pcap() {
    local suite="$1"
    local project_name="$2"
    local tshark_container="$3"
    local suite_dir="${REPORT_DIR}/${suite}"
    local tshark_id
    local tshark_image_id
    local dev_id

    if ! tshark_id="$(resolve_project_container_id "${project_name}" "${tshark_container}")"; then
        printf 'tshark container %s is absent or foreign\n' "${tshark_container}" \
            >"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if ! tshark_image_id="$("${PODMAN[@]}" inspect --type container --format '{{.Image}}' \
        "${tshark_id}" 2>"${suite_dir}/packets.err")"; then
        printf 'failed to inspect immutable tshark image ID\n' >>"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if [[ ! "${tshark_image_id}" =~ ^[0-9a-f]{64}$ ]]; then
        printf 'invalid immutable tshark image ID %q\n' "${tshark_image_id}" \
            >"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if ! "${PODMAN[@]}" image exists "${tshark_image_id}" 2>"${suite_dir}/packets.err"; then
        printf 'immutable tshark image ID is unavailable: %s\n' "${tshark_image_id}" \
            >>"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if ! dev_id="$(interop_resolve_project_service_container_id "${DEV_PROJECT}" dev)"; then
        printf 'failed to resolve exact dev container for tshark image ID persistence\n' \
            >"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if ! "${PODMAN[@]}" exec "${dev_id}" \
        go -C /app run ./test/e2e/routing/scripts/artifactmerge \
        write-image-id "/app/${REPORT_REL}" "${suite}/tshark-image-id" \
        "${tshark_image_id}" 2>"${suite_dir}/packets.err"; then
        printf 'failed to persist immutable tshark image ID\n' >>"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if ! "${PODMAN[@]}" exec "${tshark_id}" cat /captures/bfd.pcapng \
        >"${suite_dir}/packets.pcapng" 2>"${suite_dir}/packets.err"; then
        printf 'failed to copy tshark packet capture\n' >>"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.pcapng"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if [[ ! -s "${suite_dir}/packets.pcapng" ]]; then
        printf 'copied tshark packet capture is empty\n' >>"${suite_dir}/packets.err"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if ! "${PODMAN[@]}" exec "${tshark_id}" tshark -r /captures/bfd.pcapng -Y bfd \
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
        -E separator=, >"${suite_dir}/packets.csv" 2>"${suite_dir}/packets-csv.err"; then
        printf 'failed to decode tshark packet capture\n' >>"${suite_dir}/packets-csv.err"
        : >"${suite_dir}/packets.csv"
        return 1
    fi
    if [[ ! -s "${suite_dir}/packets.csv" ]]; then
        printf 'decoded tshark packet CSV is empty\n' >>"${suite_dir}/packets-csv.err"
        return 1
    fi
    if ! append_csv "${suite}" "${suite_dir}/packets.csv"; then
        printf 'failed to append tshark packet CSV\n' >>"${suite_dir}/packets-csv.err"
        return 1
    fi
}

collect_holo_diagnostics() {
    local project_name="$1"
    local compose_file="$2"
    local suite_dir="$3"
    local status=0 holo_id loader_id

    if holo_id="$(resolve_project_container_id "${project_name}" holo-interop)"; then
        "${PODMAN[@]}" logs --tail 100 "${holo_id}" >"${suite_dir}/holo.log" \
            2>"${suite_dir}/holo-log.err" || status=1
        "${PODMAN[@]}" exec "${holo_id}" sh -c \
            'if [ -f /tmp/holod.err ]; then cat /tmp/holod.err; else printf "%s\n" "/tmp/holod.err is absent"; fi' \
            >"${suite_dir}/holod.err" 2>"${suite_dir}/holod-exec.err" || status=1
    else
        : >"${suite_dir}/holo.log"
        printf '%s\n' 'holo-interop container is absent or foreign' >"${suite_dir}/holo-log.err"
        printf '%s\n' 'holo-interop container is absent' >"${suite_dir}/holod.err"
        : >"${suite_dir}/holod-exec.err"
        status=1
    fi
    if loader_id="$(resolve_project_container_id "${project_name}" holo-config-interop)"; then
        "${PODMAN[@]}" logs --tail 100 "${loader_id}" >"${suite_dir}/holo-config.log" \
            2>"${suite_dir}/holo-config-log.err" || status=1
    else
        : >"${suite_dir}/holo-config.log"
        printf '%s\n' 'holo-config-interop container is absent or foreign' \
            >"${suite_dir}/holo-config-log.err"
        status=1
    fi
    return "${status}"
}

collect_container_logs() {
    local suite="$1"
    local project_name="$2"
    shift 2
    local container_name container_id

    for container_name in "$@"; do
        if container_id="$(resolve_project_container_id "${project_name}" "${container_name}")"; then
            printf '\n===== %s container %s =====\n' "${suite}" "${container_name}"
            "${PODMAN[@]}" logs "${container_id}" || true
        fi
    done
}

fail_holo_suite_startup() {
    local project_name="$1"
    local compose_file="$2"
    local suite_dir="$3"
    local message="$4"

    printf '%s\n' "${message}" >&2
    collect_holo_diagnostics "${project_name}" "${compose_file}" "${suite_dir}" || true
    local artifact
    for artifact in holo.log holo-config.log holod.err holo-log.err holo-config-log.err holod-exec.err; do
        if [ -s "${suite_dir}/${artifact}" ]; then
            printf '\n===== %s =====\n' "${artifact}" >&2
            sed -n '1,100p' "${suite_dir}/${artifact}" >&2
        fi
    done
    return 1
}

start_holo_interop_suite() {
    local project_name="$1"
    local compose_file="$2"
    local suite_dir="$3"
    local dc=(timeout 2m podman-compose -p "${project_name}" -f "${compose_file}")
    local wait_status inspect_status loader_id semantic_error

    if ! "${dc[@]}" up -d holo holo-config; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "failed to start Holo configuration phase"
        return 1
    fi
    if ! loader_id="$(resolve_project_container_id "${project_name}" holo-config-interop)"; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "holo-config-interop is absent or not owned by ${project_name}"
        return 1
    fi
    if ! wait_status="$(timeout 30s podman wait "${loader_id}")"; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "timed out or failed waiting for holo-config-interop"
        return 1
    fi
    if [[ ! "${wait_status}" =~ ^[0-9]+$ ]]; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "invalid holo-config wait status: ${wait_status}"
        return 1
    fi
    if ! inspect_status="$(timeout 30s podman inspect --format '{{.State.ExitCode}}' "${loader_id}")"; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "failed to inspect holo-config-interop exit status"
        return 1
    fi
    if [[ ! "${inspect_status}" =~ ^[0-9]+$ ]]; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "invalid holo-config inspect status: ${inspect_status}"
        return 1
    fi
    if [ "${wait_status}" != "${inspect_status}" ]; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "holo-config status mismatch: wait=${wait_status}, inspect=${inspect_status}"
        return 1
    fi
    if [ "${wait_status}" -ne 0 ]; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "holo-config exited with status ${wait_status}"
        return 1
    fi
    semantic_error=""
    if ! semantic_error="$(interop_verify_holo_running_configuration \
        "${project_name}" "${loader_id}" 2>&1)"; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "${semantic_error:-failed to verify Holo running configuration}"
        return 1
    fi
    if ! "${dc[@]}" up -d --no-deps gobfd frr bird3 tshark thoro; then
        fail_holo_suite_startup "${project_name}" "${compose_file}" "${suite_dir}" \
            "failed to start GoBFD interop services after Holo configuration"
        return 1
    fi
}

start_generic_suite() {
    local project_name="$1"
    local compose_file="$2"
    local dc=(timeout 2m podman-compose -p "${project_name}" -f "${compose_file}")

    "${dc[@]}" up -d
}

merge_artifacts() {
    local merge_status=0
    local merge_ids
    local tshark_image_file="${REPORT_DIR}/interop/tshark-image-id"
    local bgp_tshark_image_file="${REPORT_DIR}/interop-bgp/tshark-image-id"
    local tshark_image_id=""
    local bgp_tshark_image_id=""
    local dev_id=""
    local pcap_file

    merge_ids="$(interop_query_labelled_container_ids \
        "${MERGE_OWNER_LABEL_KEY}" "${MERGE_OWNER_LABEL_VALUE}")" || return 1
    if [[ -n "${merge_ids}" ]]; then
        printf 'merge ownership label collision %s=%s\n' \
            "${MERGE_OWNER_LABEL_KEY}" "${MERGE_OWNER_LABEL_VALUE}" >&2
        return 1
    fi
    if ! dev_id="$(interop_resolve_project_service_container_id "${DEV_PROJECT}" dev)"; then
        printf 'routing artifact merge cannot resolve the exact owned dev container\n' >&2
        merge_status=1
    elif ! tshark_image_id="$("${PODMAN[@]}" exec "${dev_id}" \
        go -C /app run ./test/e2e/routing/scripts/artifactmerge \
        read-image-id "/app/${REPORT_REL}" "interop/tshark-image-id")"; then
        printf 'failed to read base tshark image ID artifact: %s\n' \
            "${tshark_image_file}" >&2
        merge_status=1
    elif [[ ! "${tshark_image_id}" =~ ^[0-9a-f]{64}$ ]]; then
        printf 'base tshark image ID artifact is invalid: %s\n' \
            "${tshark_image_file}" >&2
        merge_status=1
    else
        if ! "${PODMAN[@]}" image exists "${tshark_image_id}"; then
            printf 'base tshark image ID is unavailable: %s\n' "${tshark_image_id}" >&2
            merge_status=1
        fi
    fi
    if [[ -n "${dev_id}" ]]; then
        if ! bgp_tshark_image_id="$("${PODMAN[@]}" exec "${dev_id}" \
            go -C /app run ./test/e2e/routing/scripts/artifactmerge \
            read-image-id "/app/${REPORT_REL}" "interop-bgp/tshark-image-id")"; then
            printf 'failed to read BGP tshark image ID artifact: %s\n' \
                "${bgp_tshark_image_file}" >&2
            merge_status=1
        elif [[ ! "${bgp_tshark_image_id}" =~ ^[0-9a-f]{64}$ ]]; then
            printf 'BGP tshark image ID artifact is invalid: %s\n' \
                "${bgp_tshark_image_file}" >&2
            merge_status=1
        elif ! "${PODMAN[@]}" image exists "${bgp_tshark_image_id}"; then
            printf 'BGP tshark image ID is unavailable: %s\n' \
                "${bgp_tshark_image_id}" >&2
            merge_status=1
        fi
    fi

    if [[ -n "${dev_id}" ]] && ! "${PODMAN[@]}" exec "${dev_id}" \
        go -C /app run ./test/e2e/routing/scripts/artifactmerge \
        merge \
        "/app/${REPORT_REL}" \
        "containers.json" \
        "interop/containers.json" \
        "interop-bgp/containers.json"; then
        printf 'routing container inventory merge failed\n' >&2
        merge_status=1
    fi

    for pcap_file in \
        "${REPORT_DIR}/interop/packets.pcapng" \
        "${REPORT_DIR}/interop-bgp/packets.pcapng"; do
        if [[ -L "${pcap_file}" || ! -f "${pcap_file}" || ! -s "${pcap_file}" ]]; then
            printf 'required packet capture is missing, empty, or unsafe: %s\n' \
                "${pcap_file}" >&2
            merge_status=1
        fi
    done

    if [ "${merge_status}" -eq 0 ]; then
        if ! "${PODMAN[@]}" run \
            --label "${MERGE_OWNER_LABEL_KEY}=${MERGE_OWNER_LABEL_VALUE}" \
            --entrypoint /usr/bin/mergecap \
            -v "${REPORT_DIR}:/reports:z" "${tshark_image_id}" \
            -w /reports/packets.pcapng \
            /reports/interop/packets.pcapng \
            /reports/interop-bgp/packets.pcapng >/dev/null 2>"${REPORT_DIR}/mergecap.err"; then
            merge_status=1
        fi
    fi
    interop_remove_labelled_containers \
        "${MERGE_OWNER_LABEL_KEY}" "${MERGE_OWNER_LABEL_VALUE}" || merge_status=1
    interop_verify_labelled_containers_absent \
        "${MERGE_OWNER_LABEL_KEY}" "${MERGE_OWNER_LABEL_VALUE}" || merge_status=1
    return "${merge_status}"
}

query_project_resources() {
    local project_name="$1"
    interop_query_project_resources "${project_name}"
}

acquire_project_lock() {
    local project_name="$1"

    interop_acquire_project_lock "${project_name}" || return 1
    PROJECT_LOCK_FDS["${project_name}"]="${INTEROP_ACQUIRED_LOCK_FD}"
}

release_project_lock() {
    local project_name="$1"
    local lock_fd="${PROJECT_LOCK_FDS[${project_name}]:-}"

    if [[ -n "${lock_fd}" ]]; then
        interop_release_project_lock "${lock_fd}" || return 1
        unset "PROJECT_LOCK_FDS[${project_name}]"
    fi
}

assert_fixed_names_available() {
    local project_name="$1"
    shift
    interop_assert_fixed_names_available "${project_name}" "$@"
}

resolve_project_container_id() {
    interop_resolve_project_container_id "$1" "$2"
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
    interop_remove_project_resources "${project_name}"
}

verify_project_absent() {
    local project_name="$1"
    interop_verify_project_absent "${project_name}"
}

cleanup_project() {
    local project_name="$1"
    local status=0
    remove_project_resources "${project_name}" || status=1
    verify_project_absent "${project_name}" || status=1
    return "${status}"
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
    local startup_mode="$7"
    local project_name="$8"
    shift 8
    local containers=("$@")
    local suite_dir="${REPORT_DIR}/${suite}"
    local dc=(podman-compose -p "${project_name}" -f "${compose_file}")
    local test_status=0
    local owned_index="${#OWNED_PROJECTS[@]}"

    if ! acquire_project_lock "${project_name}"; then
        return 1
    fi
    if ! assert_project_available "${project_name}" || \
       ! assert_fixed_names_available "${project_name}" "${containers[@]}"; then
        release_project_lock "${project_name}" || true
        return 1
    fi
    OWNED_PROJECTS+=("${project_name}")
    if ! timeout 10m "${dc[@]}" build --no-cache; then
        printf 'suite %s image build failed\n' "${suite}" >&2
        test_status=1
    fi
    if [ "${test_status}" -eq 0 ]; then
        case "${startup_mode}" in
            holo)
                start_holo_interop_suite "${project_name}" "${compose_file}" "${suite_dir}" || test_status=1
                ;;
            generic)
                start_generic_suite "${project_name}" "${compose_file}" || test_status=1
                ;;
            *)
                printf 'unknown routing suite startup mode %q\n' "${startup_mode}" >&2
                test_status=1
                ;;
        esac
    fi
    if [ "${test_status}" -eq 0 ]; then
        sleep 15
    fi

    if [ "${test_status}" -eq 0 ]; then
        local dev_id
        dev_id="$(interop_resolve_project_service_container_id "${DEV_PROJECT}" dev)" || test_status=1
    fi
    if [ "${test_status}" -eq 0 ]; then
        set +e
        "${PODMAN[@]}" exec "${dev_id}" env \
            "INTEROP_PROJECT_NAME=${project_name}" \
            "${env_name}=/app/${compose_file#"${ROOT_DIR}/"}" \
            go test -tags "${tag}" -json -v -count=1 -timeout 300s "${package}" \
            | tee -a "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log" >"${suite_dir}/go-test.json"
        test_status="${PIPESTATUS[0]}"
        set -e
    else
        : >"${suite_dir}/go-test.json"
    fi

    if ! jq -s -e '[.[] | select(.Action == "pass" and has("Test"))] | length > 0' \
        "${suite_dir}/go-test.json" >/dev/null; then
        printf 'suite %s produced no passed Go tests\n' "${suite}" >&2
        test_status=1
    fi

    cp "${suite_dir}/go-test.json" "${suite_dir}/go-test.log"
    collect_container_logs "${suite}" "${project_name}" "${containers[@]}" \
        >>"${REPORT_DIR}/containers.log" 2>&1 || true
    if [ "${startup_mode}" = "holo" ]; then
        collect_holo_diagnostics "${project_name}" "${compose_file}" "${suite_dir}" || true
    fi
    if ! collect_pcap "${suite}" "${project_name}" "${tshark_container}"; then
        printf 'suite %s packet collection failed\n' "${suite}" >&2
        test_status=1
    fi
    record_containers "${suite}" "${project_name}" "${containers[@]}"
    cleanup_project "${project_name}" || test_status=1
    release_project_lock "${project_name}" || test_status=1
    unset "OWNED_PROJECTS[${owned_index}]"
    return "${test_status}"
}

cleanup() {
    local status="$?"
    trap - EXIT
    local owned project_name
    if ! merge_artifacts; then
        printf 'routing artifact merge failed\n' >&2
        status=1
    fi
    for owned in "${OWNED_PROJECTS[@]}"; do
        project_name="${owned}"
        cleanup_project "${project_name}" || status=1
        release_project_lock "${project_name}" || status=1
    done
    write_environment
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

run_suite "interop" "${ROOT_DIR}/test/interop/compose.yml" "INTEROP_COMPOSE_FILE" "interop" "./test/interop/" "tshark-interop" \
    "holo" "${INTEROP_PROJECT_NAME}" gobfd-interop frr-interop bird3-interop tshark-interop holo-interop holo-config-interop thoro-interop scapy-interop

run_suite "interop-bgp" "${ROOT_DIR}/test/interop-bgp/compose.yml" "INTEROP_BGP_COMPOSE_FILE" "interop_bgp" "./test/interop-bgp/" "tshark-bgp-interop" \
    "generic" "${INTEROP_BGP_PROJECT_NAME}" gobfd-bgp-interop gobgp-interop frr-bgp-interop bird3-bgp-interop gobfd-exabgp-interop exabgp-interop tshark-bgp-interop

printf 'S10.3 routing E2E artifacts: %s\n' "${REPORT_DIR}"

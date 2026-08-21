#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/compose.yml"
INTEROP_PROJECT_NAME="${INTEROP_PROJECT_NAME:-gobfd-interop}"
if [[ ! "${INTEROP_PROJECT_NAME}" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
    printf 'invalid INTEROP_PROJECT_NAME %q: use lowercase letters, digits, dashes, and underscores\n' \
        "${INTEROP_PROJECT_NAME}" >&2
    exit 2
fi

PODMAN=(timeout 2m podman)
DC=(timeout 2m podman-compose -p "${INTEROP_PROJECT_NAME}" -f "${COMPOSE_FILE}")
# shellcheck source=test/interop/project_guard.sh
source "${SCRIPT_DIR}/project_guard.sh"
FIXED_CONTAINER_NAMES=(
    gobfd-interop frr-interop bird3-interop tshark-interop
    holo-interop holo-config-interop thoro-interop scapy-interop
)
LOCK_FD=""
MUTATION_STARTED=false
KEEP_PROJECT=false

release_lock() {
    local status="$?"
    trap - EXIT
    if [[ "${MUTATION_STARTED}" == true && "${KEEP_PROJECT}" != true ]]; then
        if interop_fixed_names_match_project "${INTEROP_PROJECT_NAME}" "${FIXED_CONTAINER_NAMES[@]}"; then
            if ! "${DC[@]}" down --volumes --remove-orphans >/dev/null 2>&1; then
                printf 'Compose cleanup was partial; resolving exact labelled resources\n' >&2
            fi
        fi
        interop_remove_project_resources "${INTEROP_PROJECT_NAME}" || status=1
        interop_verify_project_absent "${INTEROP_PROJECT_NAME}" || status=1
    fi
    if [[ -n "${LOCK_FD}" ]]; then
        interop_release_project_lock "${LOCK_FD}" || status=1
    fi
    exit "${status}"
}
trap release_lock EXIT

acquire_lock() {
    interop_acquire_project_lock "${INTEROP_PROJECT_NAME}"
    LOCK_FD="${INTEROP_ACQUIRED_LOCK_FD}"
}

assert_empty_project() {
    local resources
    resources="$(interop_query_project_resources "${INTEROP_PROJECT_NAME}")" || return 1
    if [[ -n "${resources}" ]]; then
        printf 'Compose project %s already owns resources; refusing collision\n%s\n' \
            "${INTEROP_PROJECT_NAME}" "${resources}" >&2
        return 1
    fi
    interop_assert_fixed_names_available "${INTEROP_PROJECT_NAME}" "${FIXED_CONTAINER_NAMES[@]}"
}

start_project() {
    local loader_id wait_status inspect_status

    acquire_lock
    assert_empty_project
    MUTATION_STARTED=true
    timeout 10m podman-compose -p "${INTEROP_PROJECT_NAME}" -f "${COMPOSE_FILE}" build
    "${DC[@]}" up -d holo holo-config
    loader_id="$(interop_resolve_project_container_id "${INTEROP_PROJECT_NAME}" holo-config-interop)" || return 1
    wait_status="$(timeout 45s podman wait "${loader_id}")" || return 1
    inspect_status="$(timeout 10s podman inspect --format '{{.State.ExitCode}}' "${loader_id}")" || return 1
    if [[ ! "${wait_status}" =~ ^[0-9]+$ || ! "${inspect_status}" =~ ^[0-9]+$ || \
          "${wait_status}" != "${inspect_status}" || "${wait_status}" != "0" ]]; then
        printf 'holo-config provider gate failed: wait=%s inspect=%s\n' \
            "${wait_status}" "${inspect_status}" >&2
        return 1
    fi
    "${DC[@]}" up -d --no-deps gobfd frr bird3 tshark thoro
    KEEP_PROJECT=true
}

stop_project() {
    local status=0
    acquire_lock
    if interop_fixed_names_match_project "${INTEROP_PROJECT_NAME}" "${FIXED_CONTAINER_NAMES[@]}"; then
        if ! "${DC[@]}" down --volumes --remove-orphans >/dev/null 2>&1; then
            printf 'Compose cleanup was partial; resolving exact labelled resources\n' >&2
        fi
    else
        printf 'skipping Compose down because a fixed container name is foreign\n' >&2
    fi
    interop_remove_project_resources "${INTEROP_PROJECT_NAME}" || status=1
    interop_verify_project_absent "${INTEROP_PROJECT_NAME}" || status=1
    return "${status}"
}

logs_project() {
    local container_name container_id
    acquire_lock
    for container_name in "${FIXED_CONTAINER_NAMES[@]}"; do
        if container_id="$(interop_resolve_project_container_id "${INTEROP_PROJECT_NAME}" "${container_name}")"; then
            printf '\n===== %s =====\n' "${container_name}"
            "${PODMAN[@]}" logs "${container_id}"
        fi
    done
}

tshark_project() {
    local mode="$1"
    local tshark_id
    acquire_lock
    tshark_id="$(interop_resolve_project_container_id "${INTEROP_PROJECT_NAME}" tshark-interop)"
    case "${mode}" in
        capture)
            "${PODMAN[@]}" exec "${tshark_id}" tshark -i any -f "udp port 3784" -V
            ;;
        pcap)
            "${PODMAN[@]}" exec "${tshark_id}" tshark -r /captures/bfd.pcapng -V -Y bfd
            ;;
        summary)
            "${PODMAN[@]}" exec "${tshark_id}" tshark -r /captures/bfd.pcapng -Y bfd \
                -T fields -e frame.time_relative -e ip.src -e ip.dst \
                -e bfd.version -e bfd.diag -e bfd.sta -e bfd.flags \
                -e bfd.detect_time_multiplier -e bfd.my_discriminator \
                -e bfd.your_discriminator -e bfd.desired_min_tx_interval \
                -e bfd.required_min_rx_interval -E header=y -E separator=,
            ;;
    esac
}

case "${1:-}" in
    up) start_project ;;
    down) stop_project ;;
    logs) logs_project ;;
    capture|pcap|summary) tshark_project "$1" ;;
    *)
        printf 'usage: %s {up|down|logs|capture|pcap|summary}\n' "$0" >&2
        exit 2
        ;;
esac

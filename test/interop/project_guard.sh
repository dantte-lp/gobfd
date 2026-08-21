#!/usr/bin/env bash

# Shared exact-project ownership helpers for the legacy and E2E interop runners.
# Callers must define a bounded PODMAN array before invoking container helpers.

INTEROP_ACQUIRED_LOCK_FD=""

interop_validate_lock_directory() {
    local lock_dir="$1"
    local mode

    if [[ -L "${lock_dir}" || ! -d "${lock_dir}" || ! -O "${lock_dir}" || ! -w "${lock_dir}" ]]; then
        printf 'unsafe interop lock directory %s: require owned writable non-symlink directory\n' \
            "${lock_dir}" >&2
        return 1
    fi
    mode="$(stat -c '%a' -- "${lock_dir}")" || return 1
    if [[ "${mode}" != "700" ]]; then
        printf 'unsafe interop lock directory %s mode %s: require 700\n' \
            "${lock_dir}" "${mode}" >&2
        return 1
    fi
}

interop_validate_preferred_lock_base() {
    local runtime_base="$1"
    local mode

    if [[ -L "${runtime_base}" || ! -d "${runtime_base}" || ! -O "${runtime_base}" || ! -w "${runtime_base}" ]]; then
        printf 'unsafe preferred interop lock base %s: require owned writable non-symlink directory\n' \
            "${runtime_base}" >&2
        return 1
    fi
    mode="$(stat -c '%a' -- "${runtime_base}")" || return 1
    if [[ "${mode}" != "700" ]]; then
        printf 'unsafe preferred interop lock base %s mode %s: require 700\n' \
            "${runtime_base}" "${mode}" >&2
        return 1
    fi
}

interop_validate_fallback_lock_base() {
    local fallback_base="$1"
    local owner mode mode_value

    if [[ -L "${fallback_base}" || ! -d "${fallback_base}" || ! -w "${fallback_base}" ]]; then
        printf 'unsafe interop fallback lock base %s: require writable non-symlink directory\n' \
            "${fallback_base}" >&2
        return 1
    fi
    read -r owner mode < <(stat -c '%u %a' -- "${fallback_base}") || return 1
    mode_value=$((8#${mode}))
    if [[ "${owner}" == "${UID}" ]] && (( (mode_value & 0022) == 0 )); then
        return 0
    fi
    if [[ "${owner}" == "0" ]] && (( (mode_value & 01000) != 0 )); then
        return 0
    fi
    printf 'unsafe interop fallback lock base %s owner %s mode %s\n' \
        "${fallback_base}" "${owner}" "${mode}" >&2
    return 1
}

interop_lock_directory() {
    local runtime_base="${XDG_RUNTIME_DIR:-}"
    local user_runtime_base="/run/user/${UID}"
    local fallback_base="${TMPDIR:-/tmp}"
    local lock_dir

    if ! interop_validate_preferred_lock_base "${runtime_base}" >/dev/null 2>&1; then
        runtime_base="${user_runtime_base}"
    fi
    if ! interop_validate_preferred_lock_base "${runtime_base}" >/dev/null 2>&1; then
        runtime_base="${fallback_base}"
        interop_validate_fallback_lock_base "${runtime_base}" || return 1
    fi
    lock_dir="${runtime_base}/gobfd-interop-${UID}.locks"
    if [[ ! -e "${lock_dir}" && ! -L "${lock_dir}" ]]; then
        if ! (umask 077 && mkdir -- "${lock_dir}"); then
            if [[ ! -d "${lock_dir}" ]]; then
                printf 'create interop lock directory %s failed\n' "${lock_dir}" >&2
                return 1
            fi
        fi
    fi
    interop_validate_lock_directory "${lock_dir}" || return 1
    printf '%s\n' "${lock_dir}"
}

interop_acquire_project_lock() {
    local project_name="$1"
    local lock_dir lock_file old_umask lock_fd mode

    INTEROP_ACQUIRED_LOCK_FD=""
    lock_dir="$(interop_lock_directory)" || return 1
    lock_file="${lock_dir}/${project_name}.lock"
    if [[ -e "${lock_file}" || -L "${lock_file}" ]]; then
        if [[ -L "${lock_file}" || ! -f "${lock_file}" || ! -O "${lock_file}" ]]; then
            printf 'unsafe interop lock file %s\n' "${lock_file}" >&2
            return 1
        fi
        mode="$(stat -c '%a' -- "${lock_file}")" || return 1
        if [[ "${mode}" != "600" ]]; then
            printf 'unsafe interop lock file %s mode %s: require 600\n' \
                "${lock_file}" "${mode}" >&2
            return 1
        fi
    fi

    old_umask="$(umask)"
    umask 077
    exec {lock_fd}>"${lock_file}"
    umask "${old_umask}"
    if [[ -L "${lock_file}" || ! -f "${lock_file}" || ! -O "${lock_file}" ]]; then
        exec {lock_fd}>&-
        printf 'unsafe interop lock file %s after open\n' "${lock_file}" >&2
        return 1
    fi
    mode="$(stat -c '%a' -- "${lock_file}")" || {
        exec {lock_fd}>&-
        return 1
    }
    if [[ "${mode}" != "600" ]]; then
        exec {lock_fd}>&-
        printf 'unsafe interop lock file %s mode %s after open: require 600\n' \
            "${lock_file}" "${mode}" >&2
        return 1
    fi
    if ! flock -n "${lock_fd}"; then
        exec {lock_fd}>&-
        printf 'Compose project %s is locked by another runner\n' "${project_name}" >&2
        return 1
    fi
    # shellcheck disable=SC2034 # Public result consumed by the sourcing runner.
    INTEROP_ACQUIRED_LOCK_FD="${lock_fd}"
}

interop_release_project_lock() {
    local lock_fd="$1"
    local fd_to_close

    [[ -n "${lock_fd}" ]] || return 0
    flock -u "${lock_fd}" || return 1
    fd_to_close="${lock_fd}"
    exec {fd_to_close}>&-
}

interop_assert_fixed_names_available() {
    local project_name="$1"
    shift
    local container_name exists_status label

    for container_name in "$@"; do
        exists_status=0
        "${PODMAN[@]}" container exists "${container_name}" || exists_status=$?
        if [[ "${exists_status}" -eq 1 ]]; then
            continue
        fi
        if [[ "${exists_status}" -ne 0 ]]; then
            printf 'failed to check fixed container name %s\n' "${container_name}" >&2
            return 1
        fi
        label="$("${PODMAN[@]}" inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project" }}' \
            "${container_name}")" || return 1
        [[ -n "${label}" && "${label}" != "<no value>" ]] || label="<unlabelled>"
        printf 'fixed container name %s belongs to Compose project %s; refusing collision with %s\n' \
            "${container_name}" "${label}" "${project_name}" >&2
        return 1
    done
}

interop_assert_existing_fixed_names_owned() {
    local project_name="$1"
    shift
    local container_name exists_status label

    for container_name in "$@"; do
        exists_status=0
        "${PODMAN[@]}" container exists "${container_name}" || exists_status=$?
        if [[ "${exists_status}" -eq 1 ]]; then
            continue
        fi
        if [[ "${exists_status}" -ne 0 ]]; then
            printf 'failed to verify existing fixed container name %s\n' \
                "${container_name}" >&2
            return 1
        fi
        label="$("${PODMAN[@]}" inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project" }}' \
            "${container_name}")" || return 1
        if [[ "${label}" != "${project_name}" ]]; then
            [[ -n "${label}" && "${label}" != "<no value>" ]] || label="<unlabelled>"
            printf 'fixed container name %s belongs to Compose project %s, not the locked project\n' \
                "${container_name}" "${label}" >&2
            return 1
        fi
    done
}

interop_assert_existing_project() {
    local project_name="$1"
    shift
    local required_count="$1"
    shift
    local resources container_name index

    resources="$(interop_query_project_resources "${project_name}")" || return 1
    if [[ -z "${resources}" ]]; then
        printf 'Compose project %s has no exact-labelled resources\n' "${project_name}" >&2
        return 1
    fi
    if [[ ! "${required_count}" =~ ^[0-9]+$ || "${required_count}" -gt "$#" ]]; then
        printf 'invalid required container count %q for project %s\n' \
            "${required_count}" "${project_name}" >&2
        return 1
    fi
    for ((index = 0; index < required_count; index++)); do
        container_name="$1"
        shift
        if ! interop_resolve_project_container_id "${project_name}" "${container_name}" >/dev/null; then
            printf 'required container %s is absent or foreign for Compose project %s\n' \
                "${container_name}" "${project_name}" >&2
            return 1
        fi
    done
    # Optional one-shot/test containers (currently only Scapy) may be absent.
    interop_assert_existing_fixed_names_owned "${project_name}" "$@"
}

interop_resolve_project_container_id() {
    local project_name="$1"
    local container_name="$2"
    local exists_status=0 details container_id label

    "${PODMAN[@]}" container exists "${container_name}" || exists_status=$?
    if [[ "${exists_status}" -eq 1 ]]; then
        return 1
    fi
    if [[ "${exists_status}" -ne 0 ]]; then
        printf 'failed to resolve container %s\n' "${container_name}" >&2
        return 1
    fi
    details="$("${PODMAN[@]}" inspect --type container \
        --format '{{.ID}}|{{ index .Config.Labels "com.docker.compose.project" }}' \
        "${container_name}")" || return 1
    if [[ "${details}" != *"|"* ]]; then
        printf 'invalid ownership inspection for container %s\n' "${container_name}" >&2
        return 1
    fi
    container_id="${details%%|*}"
    label="${details#*|}"
    if [[ -z "${container_id}" || "${label}" != "${project_name}" ]]; then
        printf 'refusing foreign container %s with Compose project label %s\n' \
            "${container_name}" "${label:-<unlabelled>}" >&2
        return 1
    fi
    printf '%s\n' "${container_id}"
}

interop_resolve_project_service_container_id() {
    local project_name="$1"
    local service_name="$2"
    local container_ids container_id resolved_id count=0

    container_ids="$(timeout 30s podman ps -a --no-trunc \
        --filter "label=com.docker.compose.project=${project_name}" \
        --filter "label=com.docker.compose.service=${service_name}" \
        --format '{{.ID}}')" || return 1
    while IFS= read -r container_id; do
        [[ -n "${container_id}" ]] || continue
        count=$((count + 1))
        resolved_id="${container_id}"
    done <<<"${container_ids}"
    if [[ "${count}" -ne 1 ]]; then
        printf 'resolve Compose project %s service %s: found %s exact-labelled containers\n' \
            "${project_name}" "${service_name}" "${count}" >&2
        return 1
    fi
    printf '%s\n' "${resolved_id}"
}

interop_verify_holo_running_configuration() {
    local project_name="$1"
    local loader_id="$2"
    local loader_logs holo_id holo_version running_config
    local loader_error=""

    if ! loader_logs="$("${PODMAN[@]}" logs "${loader_id}" 2>&1)"; then
        printf 'failed to inspect Holo configuration loader logs\n' >&2
        return 1
    fi
    if grep -q '^% ' <<<"${loader_logs}"; then
        loader_error="Holo configuration loader reported parser or commit errors"
    elif [[ "${loader_logs}" =~ [^[:space:]] ]]; then
        loader_error="Holo configuration loader produced unexpected output"
    fi

    if ! holo_id="$(interop_resolve_project_container_id "${project_name}" holo-interop)"; then
        printf 'holo-interop is absent or not owned by %s\n' "${project_name}" >&2
        return 1
    fi
    if ! holo_version="$("${PODMAN[@]}" exec "${holo_id}" holo-cli --version 2>&1)"; then
        printf 'failed to inspect Holo CLI version\n' >&2
        return 1
    fi
    holo_version="${holo_version#"${holo_version%%[![:space:]]*}"}"
    holo_version="${holo_version%"${holo_version##*[![:space:]]}"}"
    if [[ "${holo_version}" != "Holo command-line interface 0.5.0" ]]; then
        printf 'unexpected Holo CLI version: %s\n' "${holo_version}" >&2
        return 1
    fi

    if ! running_config="$("${PODMAN[@]}" exec "${holo_id}" \
        holo-cli --no-colors --no-pager \
        --address http://127.0.0.1:50051 \
        --command 'show running format json' 2>&1)"; then
        printf 'failed to inspect Holo running configuration\n' >&2
        return 1
    fi
    if ! jq -s -e '
        length == 1
        and (.[0]
          | [
              .["ietf-interfaces:interfaces"]?
              | select(type == "object")
              | .interface?
              | select(type == "array")
              | .[]
              | select(
                  type == "object"
                  and .name == "eth0"
                  and .type == "iana-if-type:ethernetCsmacd"
                  and (.["ietf-ip:ipv4"] | type) == "object"
                )
            ] as $interfaces
          | [
              .["ietf-routing:routing"]?
              | select(type == "object")
              | .["control-plane-protocols"]?
              | select(type == "object")
              | .["control-plane-protocol"]?
              | select(type == "array")
              | .[]
              | select(
                  type == "object"
                  and .type == "ietf-bfd-types:bfdv1"
                  and .name == "main"
                )
            ] as $protocols
          | [
              $protocols[]?
              | .["ietf-bfd:bfd"]?
              | select(type == "object")
              | .["ietf-bfd-ip-sh:ip-sh"]?
              | select(type == "object")
              | .sessions?
              | select(type == "object")
              | .session?
              | select(type == "array")
              | .[]
              | select(
                  type == "object"
                  and .interface == "eth0"
                  and .["dest-addr"] == "172.20.0.10"
                  and .["source-addr"] == "172.20.0.50"
                  and .["local-multiplier"] == 3
                  and .["desired-min-tx-interval"] == 300000
                  and .["required-min-rx-interval"] == 300000
                )
            ] as $sessions
          | ($interfaces | length) == 1
            and ($protocols | length) == 1
            and ($sessions | length) == 1)
    ' >/dev/null 2>&1 <<<"${running_config}"; then
        printf 'Holo running configuration is missing the required BFD session\n' >&2
        return 1
    fi
    if [[ -n "${loader_error}" ]]; then
        printf '%s\n' "${loader_error}" >&2
        return 1
    fi
}

interop_query_project_resources() {
    local project_name="$1"
    local project_label="com.docker.compose.project=${project_name}"
    local containers networks volumes id

    containers="$(timeout 30s podman ps -a --no-trunc --filter "label=${project_label}" --format '{{.ID}}')" || return 1
    networks="$(timeout 30s podman network ls --no-trunc --filter "label=${project_label}" --format '{{.ID}}')" || return 1
    volumes="$(timeout 30s podman volume ls --filter "label=${project_label}" --format '{{.Name}}')" || return 1
    while IFS= read -r id; do
        [[ -n "${id}" ]] && printf 'container:%s\n' "${id}"
    done <<<"${containers}"
    while IFS= read -r id; do
        [[ -n "${id}" ]] && printf 'network:%s\n' "${id}"
    done <<<"${networks}"
    while IFS= read -r id; do
        [[ -n "${id}" ]] && printf 'volume:%s\n' "${id}"
    done <<<"${volumes}"
    return 0
}

interop_query_labelled_container_ids() {
    local label_key="$1"
    local label_value="$2"

    timeout 30s podman ps -a --no-trunc \
        --filter "label=${label_key}=${label_value}" --format '{{.ID}}'
}

interop_remove_container_snapshot() {
    local -a remaining=("$@")
    local -a next=()
    local container_id exists_status progress pass
    local max_passes="${#remaining[@]}"

    for ((pass = 1; pass <= max_passes && ${#remaining[@]} > 0; pass++)); do
        next=()
        progress=0
        for container_id in "${remaining[@]}"; do
            timeout 30s podman rm -f -- "${container_id}" >/dev/null || true
            exists_status=0
            timeout 30s podman container exists "${container_id}" || exists_status=$?
            case "${exists_status}" in
                0)
                    next+=("${container_id}")
                    ;;
                1)
                    progress=1
                    ;;
                *)
                    printf 'failed to verify exact container ID %s after removal attempt\n' \
                        "${container_id}" >&2
                    return 1
                    ;;
            esac
        done
        if [[ "${#next[@]}" -eq 0 ]]; then
            return 0
        fi
        if [[ "${progress}" -eq 0 ]]; then
            printf 'no progress removing exact container snapshot; remaining IDs:' >&2
            printf ' %s' "${next[@]}" >&2
            printf '\n' >&2
            return 1
        fi
        remaining=("${next[@]}")
    done

    if [[ "${#remaining[@]}" -ne 0 ]]; then
        printf 'bounded exact container cleanup exhausted; remaining IDs:' >&2
        printf ' %s' "${remaining[@]}" >&2
        printf '\n' >&2
        return 1
    fi
}

interop_remove_labelled_containers() {
    local label_key="$1"
    local label_value="$2"
    local container_ids container_id
    local -a snapshot=()

    container_ids="$(interop_query_labelled_container_ids "${label_key}" "${label_value}")" || return 1
    while IFS= read -r container_id; do
        [[ -n "${container_id}" ]] || continue
        snapshot+=("${container_id}")
    done <<<"${container_ids}"
    interop_validate_container_snapshot \
        "${label_key}" "${label_value}" "${snapshot[@]}" || return 1
    interop_remove_container_snapshot "${snapshot[@]}"
}

interop_verify_labelled_containers_absent() {
    local label_key="$1"
    local label_value="$2"
    local container_ids

    container_ids="$(interop_query_labelled_container_ids "${label_key}" "${label_value}")" || return 1
    if [[ -n "${container_ids}" ]]; then
        printf 'owned-container leak for label %s=%s:\n%s\n' \
            "${label_key}" "${label_value}" "${container_ids}" >&2
        return 1
    fi
}

interop_validate_container_snapshot() {
    local label_key="$1"
    local label_value="$2"
    shift 2
    local container_id inspect_json

    for container_id in "$@"; do
        if ! inspect_json="$(timeout 30s podman inspect --type container \
            --format '{{json .}}' "${container_id}" 2>/dev/null)"; then
            printf 'failed to inspect exact container ID %s before cleanup\n' \
                "${container_id}" >&2
            return 1
        fi
        if ! jq -s -e \
            --arg container_id "${container_id}" \
            --arg label_key "${label_key}" \
            --arg label_value "${label_value}" '
                length == 1
                and (.[0]
                  | type == "object"
                    and .Id == $container_id
                    and (.Config.Labels | type) == "object"
                    and .Config.Labels[$label_key] == $label_value
                    and (.Mounts | type) == "array"
                    and all(.Mounts[];
                        type == "object" and (.Type | type) == "string")
                    and all(.Mounts[]; .Type != "volume"))
            ' >/dev/null 2>&1 <<<"${inspect_json}"; then
            printf 'container ownership or volume-mount preflight failed for exact ID %s\n' \
                "${container_id}" >&2
            return 1
        fi
    done
}

interop_remove_project_resources() {
    local project_name="$1"
    local resources kind resource_id
    local -a container_ids=()
    local -a network_ids=()
    local -a volume_names=()

    resources="$(interop_query_project_resources "${project_name}")" || return 1
    while IFS=: read -r kind resource_id; do
        [[ -n "${kind}" ]] || continue
        case "${kind}" in
            container)
                container_ids+=("${resource_id}")
                ;;
            network)
                network_ids+=("${resource_id}")
                ;;
            volume)
                volume_names+=("${resource_id}")
                ;;
        esac
    done <<<"${resources}"

    if [[ "${#volume_names[@]}" -ne 0 ]]; then
        printf 'guarded interop projects must use container storage or bind mounts; refusing mutable labelled volumes for Compose project %s:' \
            "${project_name}" >&2
        printf ' %s' "${volume_names[@]}" >&2
        printf '\n' >&2
        return 1
    fi
    interop_validate_container_snapshot \
        com.docker.compose.project "${project_name}" "${container_ids[@]}" || return 1
    interop_remove_container_snapshot "${container_ids[@]}" || return 1
    for resource_id in "${network_ids[@]}"; do
        timeout 30s podman network rm -- "${resource_id}" >/dev/null || true
    done
    interop_verify_project_absent "${project_name}"
}

interop_verify_project_absent() {
    local project_name="$1"
    local resources

    resources="$(interop_query_project_resources "${project_name}")" || return 1
    if [[ -n "${resources}" ]]; then
        printf 'owned-resource leak for Compose project %s:\n%s\n' \
            "${project_name}" "${resources}" >&2
        return 1
    fi
}

interop_cleanup_project_resources() {
    local project_name="$1"
    local status=0

    interop_remove_project_resources "${project_name}" || status=1
    interop_verify_project_absent "${project_name}" || status=1
    return "${status}"
}

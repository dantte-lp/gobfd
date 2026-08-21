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

interop_lock_directory() {
    local runtime_base="${XDG_RUNTIME_DIR:-}"
    local user_runtime_base="/run/user/${UID}"
    local fallback_base="${TMPDIR:-/tmp}"
    local lock_dir

    if [[ -L "${runtime_base}" || ! -d "${runtime_base}" || ! -O "${runtime_base}" || ! -w "${runtime_base}" ]]; then
        runtime_base="${user_runtime_base}"
    fi
    if [[ -L "${runtime_base}" || ! -d "${runtime_base}" || ! -O "${runtime_base}" || ! -w "${runtime_base}" ]]; then
        runtime_base="${fallback_base}"
    fi
    if [[ -L "${runtime_base}" || ! -d "${runtime_base}" || ! -w "${runtime_base}" ]]; then
        printf 'no writable runtime directory for interop project locks\n' >&2
        return 1
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

interop_fixed_names_match_project() {
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
            printf 'failed to revalidate fixed container name %s before cleanup\n' \
                "${container_name}" >&2
            return 1
        fi
        label="$("${PODMAN[@]}" inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project" }}' \
            "${container_name}")" || return 1
        if [[ "${label}" != "${project_name}" ]]; then
            [[ -n "${label}" && "${label}" != "<no value>" ]] || label="<unlabelled>"
            printf 'fixed container name %s changed ownership to Compose project %s; skipping Compose down\n' \
                "${container_name}" "${label}" >&2
            return 1
        fi
    done
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

interop_remove_project_resources() {
    local project_name="$1"
    local resources kind resource_id status=0

    resources="$(interop_query_project_resources "${project_name}")" || return 1
    while IFS=: read -r kind resource_id; do
        [[ -n "${kind}" ]] || continue
        case "${kind}" in
            container)
                timeout 30s podman rm -f -- "${resource_id}" >/dev/null || status=1
                ;;
            network)
                timeout 30s podman network rm -- "${resource_id}" >/dev/null || status=1
                ;;
            volume)
                timeout 30s podman volume rm -- "${resource_id}" >/dev/null || status=1
                ;;
        esac
    done <<<"${resources}"
    return "${status}"
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

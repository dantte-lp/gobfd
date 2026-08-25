#!/usr/bin/env bash
set -euo pipefail

DOCKER_COMPOSE_VERSION="${DOCKER_COMPOSE_VERSION:-5.5.0}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

apt_update() {
    sudo apt-get -o Acquire::Retries=3 update
}

apt_install() {
    sudo apt-get -o Acquire::Retries=3 install -y --no-install-recommends "$@"
}

ensure_podman() {
    if command -v podman >/dev/null 2>&1; then
        return
    fi

    apt_update
    apt_install podman
}

configure_compose_provider() {
    export PATH="${HOME}/.local/bin:${PATH}"
    export PODMAN_COMPOSE_PROVIDER="${HOME}/.local/bin/docker-compose"
    export PODMAN_COMPOSE_WARNING_LOGS=false
    export DOCKER_BUILDKIT=0
    if [ -n "${GITHUB_PATH:-}" ]; then
        printf '%s\n' "${HOME}/.local/bin" >>"${GITHUB_PATH}"
    fi
    if [ -n "${GITHUB_ENV:-}" ]; then
        {
            printf 'PODMAN_COMPOSE_PROVIDER=%s\n' "${PODMAN_COMPOSE_PROVIDER}"
            printf 'PODMAN_COMPOSE_WARNING_LOGS=false\n'
            printf 'DOCKER_BUILDKIT=0\n'
        } >>"${GITHUB_ENV}"
    fi
}

ensure_podman
DOCKER_COMPOSE_VERSION="${DOCKER_COMPOSE_VERSION}" \
    COMPOSE_INSTALL_DIR="${HOME}/.local/bin" \
    "${PROJECT_DIR}/scripts/install-compose-provider.sh"
configure_compose_provider

actual_version="$(podman compose version --short)"
if [ "${actual_version}" != "${DOCKER_COMPOSE_VERSION}" ]; then
    printf 'Podman Compose provider version %s, want %s\n' \
        "${actual_version}" "${DOCKER_COMPOSE_VERSION}" >&2
    exit 1
fi

podman --version
podman compose version

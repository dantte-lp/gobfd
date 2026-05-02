#!/usr/bin/env bash
set -euo pipefail

PODMAN_COMPOSE_VERSION="${PODMAN_COMPOSE_VERSION:-1.5.0}"

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

add_user_bin_to_path() {
    export PATH="${HOME}/.local/bin:${PATH}"
    if [ -n "${GITHUB_PATH:-}" ]; then
        printf '%s\n' "${HOME}/.local/bin" >>"${GITHUB_PATH}"
    fi
}

install_podman_compose_from_pip() {
    if ! command -v python3 >/dev/null 2>&1; then
        return 1
    fi
    if ! python3 -m pip --version >/dev/null 2>&1; then
        return 1
    fi

    python3 -m pip install --user --break-system-packages --no-cache-dir \
        "podman-compose==${PODMAN_COMPOSE_VERSION}"
    add_user_bin_to_path
}

ensure_podman_compose() {
    if command -v podman-compose >/dev/null 2>&1; then
        return
    fi

    if install_podman_compose_from_pip; then
        return
    fi

    apt_update
    apt_install python3-pip podman-compose
    add_user_bin_to_path
}

ensure_podman
ensure_podman_compose

podman --version
podman-compose --version

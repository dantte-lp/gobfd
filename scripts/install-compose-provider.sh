#!/usr/bin/env bash
set -euo pipefail

DOCKER_COMPOSE_VERSION="${DOCKER_COMPOSE_VERSION:-5.5.0}"
COMPOSE_INSTALL_DIR="${COMPOSE_INSTALL_DIR:-${HOME}/.local/bin}"

case "$(uname -m)" in
    x86_64)
        compose_asset="docker-compose-linux-x86_64"
        compose_sha256="c57ab918abd5b05ca7e7d0f275875dd1330a695074f309dc9eab1b49efafcd4b"
        ;;
    aarch64 | arm64)
        compose_asset="docker-compose-linux-aarch64"
        compose_sha256="ff42489f5a9b879d5d117c5ffea6defc27390b3286da8ad52cbc9c6ab5df590e"
        ;;
    *)
        printf 'unsupported Docker Compose architecture: %s\n' "$(uname -m)" >&2
        exit 1
        ;;
esac

install -d -m 0755 "${COMPOSE_INSTALL_DIR}"
compose_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gobfd-compose-provider.XXXXXX")"
cleanup_compose_tmp() {
    unlink "${compose_tmp}/${compose_asset}" 2>/dev/null || true
    rmdir "${compose_tmp}" 2>/dev/null || true
}
trap cleanup_compose_tmp EXIT

compose_url="https://github.com/docker/compose/releases/download/v${DOCKER_COMPOSE_VERSION}/${compose_asset}"
curl --fail --location --retry 3 --silent --show-error \
    --output "${compose_tmp}/${compose_asset}" "${compose_url}"
printf '%s  %s\n' "${compose_sha256}" "${compose_tmp}/${compose_asset}" \
    | sha256sum --check --status

compose_target="${COMPOSE_INSTALL_DIR}/docker-compose"
install -m 0755 "${compose_tmp}/${compose_asset}" "${compose_target}"
actual_version="$(${compose_target} version --short)"
if [ "${actual_version}" != "${DOCKER_COMPOSE_VERSION}" ]; then
    printf 'Docker Compose provider version %s, want %s\n' \
        "${actual_version}" "${DOCKER_COMPOSE_VERSION}" >&2
    exit 1
fi

printf 'installed Docker Compose provider %s at %s\n' \
    "${actual_version}" "${compose_target}"

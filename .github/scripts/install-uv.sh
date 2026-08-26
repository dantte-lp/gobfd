#!/usr/bin/env bash
set -euo pipefail

UV_VERSION="${UV_VERSION:-0.12.6}"
UV_INSTALL_DIR="${UV_INSTALL_DIR:-${HOME}/.local/bin}"

case "$(uname -m)" in
    x86_64 | amd64)
        uv_target="x86_64-unknown-linux-gnu"
        uv_sha256="8681d8921e7d520fb368991dcf5f9c1905b80f5bf2a265a0ed085c8d8e342477"
        ;;
    aarch64 | arm64)
        uv_target="aarch64-unknown-linux-gnu"
        uv_sha256="d58030acd26159499ac82f32da12d1b3c12a3a1bfc414232d9082070c03e128d"
        ;;
    *)
        printf 'install-uv: unsupported architecture: %s\n' "$(uname -m)" >&2
        exit 1
        ;;
esac

uv_archive="uv-${uv_target}.tar.gz"
uv_url="https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/${uv_archive}"
uv_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gobfd-uv.XXXXXXXX")"
trap 'rm -rf -- "${uv_tmp}"' EXIT

curl --fail --location --proto '=https' --proto-redir '=https' --retry 3 \
    --output "${uv_tmp}/${uv_archive}" "${uv_url}"
printf '%s  %s\n' "${uv_sha256}" "${uv_tmp}/${uv_archive}" | sha256sum --check --strict
tar --no-same-owner -xzf "${uv_tmp}/${uv_archive}" -C "${uv_tmp}"
install -d -m 0755 "${UV_INSTALL_DIR}"
install -m 0755 "${uv_tmp}/uv-${uv_target}/uv" "${UV_INSTALL_DIR}/uv"
install -m 0755 "${uv_tmp}/uv-${uv_target}/uvx" "${UV_INSTALL_DIR}/uvx"

if [[ -n "${GITHUB_PATH:-}" ]]; then
    printf '%s\n' "${UV_INSTALL_DIR}" >>"${GITHUB_PATH}"
fi

"${UV_INSTALL_DIR}/uv" --version

#!/usr/bin/env bash
# Install a verified containerlab release if it is not already present.
#
# containerlab is a multi-vendor network lab orchestration tool.
# https://containerlab.dev
#
# Usage:
#   ./test/interop-clab/install-containerlab.sh

set -euo pipefail

readonly CONTAINERLAB_VERSION="0.79.0"

if command -v containerlab &>/dev/null; then
    if ! installed_version="$(containerlab version --short 2>/dev/null)"; then
        echo "failed to determine installed containerlab version" >&2
        exit 1
    fi
    if [[ "${installed_version}" != "${CONTAINERLAB_VERSION}" ]]; then
        echo "installed containerlab version ${installed_version} does not match required ${CONTAINERLAB_VERSION}" >&2
        exit 1
    fi
    echo "containerlab ${installed_version} is already installed"
    exit 0
fi

case "$(uname -s)/$(uname -m)" in
    Linux/x86_64)
        archive="containerlab_${CONTAINERLAB_VERSION}_linux_amd64.tar.gz"
        checksum="f90d36d58bb6c4afd3b3a4dca006b81594c6d16f7a04be0184b03f44291085a2"
        ;;
    Linux/aarch64 | Linux/arm64)
        archive="containerlab_${CONTAINERLAB_VERSION}_linux_arm64.tar.gz"
        checksum="e89e051f71fad7ad1c74c0189fb260539c1f5637e5c10fe53f74fa6b4dbe5298"
        ;;
    *)
        echo "unsupported platform: $(uname -s)/$(uname -m)" >&2
        exit 1
        ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

echo "installing containerlab v${CONTAINERLAB_VERSION}..."
curl --fail --location --silent --show-error \
    "https://github.com/srl-labs/containerlab/releases/download/v${CONTAINERLAB_VERSION}/${archive}" \
    --output "${tmp_dir}/${archive}"
printf '%s  %s\n' "${checksum}" "${tmp_dir}/${archive}" | sha256sum --check --status
tar -xzf "${tmp_dir}/${archive}" -C "${tmp_dir}" containerlab
sudo install -m 0755 "${tmp_dir}/containerlab" /usr/local/bin/containerlab

installed_version="$(containerlab version --short 2>/dev/null)"
if [[ "${installed_version}" != "${CONTAINERLAB_VERSION}" ]]; then
    echo "installed containerlab version ${installed_version} does not match required ${CONTAINERLAB_VERSION}" >&2
    exit 1
fi

echo "containerlab ${installed_version} installed"

#!/usr/bin/env bash
# S10.6 optional vendor profile runner. Assertions run inside the Podman dev container.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
REPORT_REL="reports/e2e/vendor/${RUN_ID}"
REPORT_DIR="${ROOT_DIR}/${REPORT_REL}"
DEV_PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "${ROOT_DIR}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$//')}"
DEV_COMPOSE="${ROOT_DIR}/deployments/compose/compose.dev.yml"

mkdir -p "${REPORT_DIR}"

DEV_DC=(podman-compose -p "${DEV_PROJECT}" -f "${DEV_COMPOSE}")

write_vendor_images() {
    local profile
    while IFS= read -r profile; do
        local id profile_class skip_policy images available status reason image
        id="$(jq -r '.id' <<<"${profile}")"
        profile_class="$(jq -r '.profile_class' <<<"${profile}")"
        skip_policy="$(jq -r '.skip_policy' <<<"${profile}")"
        images="$(jq -c '.images' <<<"${profile}")"
        available="[]"

        while IFS= read -r image; do
            if podman image exists "${image}" 2>/dev/null; then
                available="$(jq -c --arg image "${image}" '. + [$image]' <<<"${available}")"
            fi
        done < <(jq -r '.images[]' <<<"${profile}")

        if [ "$(jq 'length' <<<"${available}")" -gt 0 ]; then
            status="available"
            reason=""
        else
            status="skipped"
            reason="${skip_policy}"
        fi

        jq -cn \
            --arg profile_id "${id}" \
            --arg profile_class "${profile_class}" \
            --arg status "${status}" \
            --arg reason "${reason}" \
            --argjson images "${images}" \
            --argjson available "${available}" \
            '{profile_id: $profile_id, profile_class: $profile_class, images: $images, available: $available, status: $status, reason: $reason}'
    done < <(jq -c '.profiles[]' "${ROOT_DIR}/test/e2e/vendor/profiles.json") \
        | jq -s . >"${REPORT_DIR}/vendor-images.json"
}

write_environment() {
    cat >"${REPORT_DIR}/environment.json" <<EOF_ENV
{
  "target": "e2e-vendor",
  "run_id": "${RUN_ID}",
  "dev_project": "${DEV_PROJECT}",
  "podman_runtime": "podman",
  "containerlab_runtime": "podman",
  "topology": "test/interop-clab/gobfd-vendors.clab.yml",
  "public_ci_default": "skip-topology"
}
EOF_ENV
}

write_summary() {
    local status="$1"
    cat >"${REPORT_DIR}/summary.md" <<EOF_SUMMARY
# e2e-vendor Summary

| Field | Value |
|---|---|
| Target | \`make e2e-vendor\` |
| Run ID | \`${RUN_ID}\` |
| Exit code | \`${status}\` |
| Runtime | \`podman\` |
| Containerlab runtime | \`podman\` |
| Topology | \`test/interop-clab/gobfd-vendors.clab.yml\` |
| Go test JSON | \`go-test.json\` |
| Go test log | \`go-test.log\` |
| Container state | \`containers.json\` |
| Container logs | \`containers.log\` |
| Vendor profiles | \`vendor-profiles.json\` |
| Image availability | \`vendor-images.json\` |
| Skip summary | \`skip-summary.json\` |
| Primary profiles | \`arista-ceos,nokia-srlinux,sonic-vs,vyos\` |
| Deferred profiles | \`cisco-xrd\` |
EOF_SUMMARY
}

cleanup() {
    local status="$?"
    podman ps -a --filter "label=io.podman.compose.project=${DEV_PROJECT}" --format json \
        >"${REPORT_DIR}/containers.json" 2>"${REPORT_DIR}/containers.err" || true
    "${DEV_DC[@]}" logs >"${REPORT_DIR}/containers.log" 2>&1 || true
    write_environment
    write_summary "${status}"
    exit "${status}"
}

trap cleanup EXIT

write_vendor_images

"${DEV_DC[@]}" exec -T dev env \
    E2E_VENDOR_REPORT_DIR="/app/${REPORT_REL}" \
    go test -tags e2e_vendor -json -v -count=1 -timeout 120s ./test/e2e/vendor/ \
    | tee "${REPORT_DIR}/go-test.json" "${REPORT_DIR}/go-test.log"

printf 'S10.6 vendor E2E artifacts: %s\n' "${REPORT_DIR}"

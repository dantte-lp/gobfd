#!/usr/bin/env bash
# Vendor BFD interoperability test runner.
#
# Tests GoBFD against commercial/enterprise NOS vendors:
#   Arista cEOS, Nokia SR Linux, Cisco XRd, SONiC-VS, VyOS, FRRouting
#
# Deploys a star topology with GoBFD in the center and point-to-point /30
# links to each available vendor NOS. Containers and veth links are managed
# directly via Podman for maximum compatibility.
#
# Usage:
#   ./test/interop-clab/run.sh            # full cycle: build → deploy → test → destroy
#   ./test/interop-clab/run.sh --up-only  # build + deploy, skip tests
#   ./test/interop-clab/run.sh --test-only # run Go tests (topology must be up)
#
# Prerequisites:
#   - podman installed, Podman socket active
#   - At least one vendor image available (Nokia SR Linux is freely available)
#
# Exit codes:
#   0 - all tests passed (or --up-only completed)
#   1 - failure

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
LAB_NAME="gobfd-vendors"
GOBFD_CONTAINER="clab-${LAB_NAME}-gobfd"
GOBFD_IMAGE="gobfd-clab:latest"
PODMAN_NETWORK="${LAB_NAME}-net"

# Deployed vendor tracking (populated by deploy_vendors).
DEPLOYED_VENDORS=()

# Vendor definitions: name:container:image:eth_idx:peer_ip:local_ip:asn
# eth_idx determines the interface name on GoBFD side (eth${idx}).
VENDOR_DEFS=(
    "arista:clab-${LAB_NAME}-arista:ceos:4.35.2F:1:10.0.1.2:10.0.1.1:65002"
    "nokia:clab-${LAB_NAME}-nokia:ghcr.io/nokia/srlinux:25.10.2:2:10.0.2.2:10.0.2.1:65003"
    "cisco:clab-${LAB_NAME}-cisco:ios-xr/xrd-control-plane:24.3.1:3:10.0.3.2:10.0.3.1:65004"
    "sonic:clab-${LAB_NAME}-sonic:docker-sonic-vs:latest:4:10.0.4.2:10.0.4.1:65005"
    "vyos:clab-${LAB_NAME}-vyos:vyos:latest:5:10.0.5.2:10.0.5.1:65006"
    "frr:clab-${LAB_NAME}-frr:quay.io/frrouting/frr:10.2.5:6:10.0.6.2:10.0.6.1:65007"
)

# IPv6 dual-stack addresses (RFC 4193 ULA, /127 per RFC 6164).
declare -A VENDOR_IPV6_PEER VENDOR_IPV6_LOCAL VENDOR_IPV6_ROUTE
VENDOR_IPV6_PEER[arista]="fd00:0:1::1"
VENDOR_IPV6_LOCAL[arista]="fd00:0:1::"
VENDOR_IPV6_ROUTE[arista]="fd00:20:1::/48"
VENDOR_IPV6_PEER[nokia]="fd00:0:2::1"
VENDOR_IPV6_LOCAL[nokia]="fd00:0:2::"
VENDOR_IPV6_ROUTE[nokia]="fd00:20:2::/48"
VENDOR_IPV6_PEER[frr]="fd00:0:6::1"
VENDOR_IPV6_LOCAL[frr]="fd00:0:6::"
VENDOR_IPV6_ROUTE[frr]="fd00:20:6::/48"

# Parse flags.
UP_ONLY=false
TEST_ONLY=false
for arg in "$@"; do
    case "${arg}" in
        --up-only)   UP_ONLY=true ;;
        --test-only) TEST_ONLY=true ;;
    esac
done

# Colors for output (disabled if not a terminal).
if [ -t 1 ]; then
    GREEN='\033[0;32m'
    RED='\033[0;31m'
    YELLOW='\033[0;33m'
    NC='\033[0m'
else
    GREEN=''
    RED=''
    YELLOW=''
    NC=''
fi

pass() { echo -e "${GREEN}PASS${NC}: $1"; }
fail() { echo -e "${RED}FAIL${NC}: $1"; }
info() { echo -e "${YELLOW}INFO${NC}: $1"; }

# ---------------------------------------------------------------------------
# Prerequisite checks
# ---------------------------------------------------------------------------

check_prerequisites() {
    if ! command -v podman &>/dev/null; then
        fail "podman not found"
        exit 1
    fi

    # Check Podman socket.
    local socket="/run/podman/podman.sock"
    if [ ! -S "${socket}" ]; then
        local user_socket="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock"
        if [ ! -S "${user_socket}" ]; then
            fail "Podman socket not active — start with: systemctl --user start podman.socket"
            exit 1
        fi
    fi

    info "prerequisites OK: podman $(podman --version)"
}

# ---------------------------------------------------------------------------
# Vendor definition helpers
# ---------------------------------------------------------------------------

vendor_field() {
    local def="$1" field="$2"
    echo "${def}" | cut -d: -f"${field}"
}

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

cleanup() {
    info "cleaning up topology"

    # Remove vendor containers.
    for vdef in "${VENDOR_DEFS[@]}"; do
        local cname
        cname="$(vendor_field "${vdef}" 2)"
        podman rm -f "${cname}" 2>/dev/null || true
    done

    # Remove GoBFD container.
    podman rm -f "${GOBFD_CONTAINER}" 2>/dev/null || true

    # Remove veth pairs (auto-removed when container is destroyed, but clean up orphans).
    for vdef in "${VENDOR_DEFS[@]}"; do
        local idx
        idx="$(vendor_field "${vdef}" 5)"
        ip link del "veth-eth${idx}" 2>/dev/null || true
    done

    # Remove network.
    podman network rm "${PODMAN_NETWORK}" 2>/dev/null || true
}

if [ "${TEST_ONLY}" = false ]; then
    trap cleanup EXIT
fi

# ---------------------------------------------------------------------------
# Phase 1: Build GoBFD image
# ---------------------------------------------------------------------------

build_gobfd_image() {
    info "building GoBFD vendor-interop image"
    podman build -t "${GOBFD_IMAGE}" \
        -f "${SCRIPT_DIR}/Containerfile.gobfd" \
        "${PROJECT_ROOT}"
}

# ---------------------------------------------------------------------------
# Phase 2: Start GoBFD container
# ---------------------------------------------------------------------------

start_gobfd_container() {
    podman rm -f "${GOBFD_CONTAINER}" 2>/dev/null || true

    info "starting GoBFD container: ${GOBFD_CONTAINER}"
    podman run -d \
        --name "${GOBFD_CONTAINER}" \
        --cap-add NET_RAW \
        --cap-add NET_ADMIN \
        --user 0:0 \
        --entrypoint '["sleep", "infinity"]' \
        "${GOBFD_IMAGE}"

    sleep 2
    if ! podman ps --format '{{.Names}}' | grep -q "${GOBFD_CONTAINER}"; then
        fail "GoBFD container failed to start"
        podman logs "${GOBFD_CONTAINER}" 2>&1 | tail -20 || true
        exit 1
    fi
    pass "GoBFD container running (idle)"
}

# ---------------------------------------------------------------------------
# Phase 3: Deploy vendor containers and create veth links
# ---------------------------------------------------------------------------

# get_container_pid returns the PID of a running container.
get_container_pid() {
    podman inspect --format '{{.State.Pid}}' "$1" 2>/dev/null
}

# create_veth_link creates a veth pair and moves endpoints into container namespaces.
# Usage: create_veth_link <gobfd_if> <vendor_if> <gobfd_pid> <vendor_pid> <local_ip/mask> <peer_ip/mask>
create_veth_link() {
    local gobfd_if="$1" vendor_if="$2" gobfd_pid="$3" vendor_pid="$4"
    local local_ip="$5" peer_ip="$6"
    local veth_a="veth-${gobfd_if}" veth_b="vpeer-${vendor_if}"

    # Create veth pair in the host namespace.
    ip link add "${veth_a}" type veth peer name "${veth_b}"

    # Move gobfd end into GoBFD container namespace.
    ip link set "${veth_a}" netns "${gobfd_pid}"
    nsenter -t "${gobfd_pid}" -n ip link set "${veth_a}" name "${gobfd_if}"
    nsenter -t "${gobfd_pid}" -n ip addr add "${local_ip}" dev "${gobfd_if}"
    nsenter -t "${gobfd_pid}" -n ip link set "${gobfd_if}" up
    # Disable TX checksum offload — veth pairs don't have hardware offload,
    # causing bad checksums that remote TCP stacks (Nokia SRL) reject.
    nsenter -t "${gobfd_pid}" -n ethtool -K "${gobfd_if}" tx off 2>/dev/null || true

    # Move vendor end into vendor container namespace.
    ip link set "${veth_b}" netns "${vendor_pid}"
    nsenter -t "${vendor_pid}" -n ip link set "${veth_b}" name "${vendor_if}"
    nsenter -t "${vendor_pid}" -n ip link set "${vendor_if}" up
}

# start_nokia_srl starts Nokia SR Linux container.
# Configuration is applied later via configure_nokia_srl after the veth
# link is created, because SR Linux must discover the interface before
# it can be configured.
start_nokia_srl() {
    local cname="$1" image="$2"

    info "starting Nokia SR Linux: ${cname}"
    podman run -t -d \
        --name "${cname}" \
        --cap-add NET_ADMIN \
        --cap-add SYS_ADMIN \
        --cap-add NET_RAW \
        --privileged \
        --user 0:0 \
        "${image}" \
        sudo bash /opt/srlinux/bin/sr_linux
}

# start_arista_ceos starts Arista cEOS container with required environment variables.
# cEOS requires 8 mandatory env vars and /sbin/init entrypoint (systemd inside).
# Environment variables must also be propagated via systemd.setenv= kernel cmdline.
# Based on containerlab's nodes/ceos/ceos.go configuration.
start_arista_ceos() {
    local cname="$1" image="$2"

    info "starting Arista cEOS: ${cname}"
    podman run -d \
        --name "${cname}" \
        --privileged \
        -e CEOS=1 \
        -e EOS_PLATFORM=ceoslab \
        -e "container=docker" \
        -e ETBA=1 \
        -e SKIP_ZEROTOUCH_BARRIER_IN_SYSDBINIT=1 \
        -e INTFTYPE=eth \
        -e MAPETH0=1 \
        -e MGMT_INTF=eth0 \
        -v "${SCRIPT_DIR}/arista/startup-config.cfg:/mnt/flash/startup-config:z" \
        "${image}" \
        /sbin/init \
        systemd.setenv=CEOS=1 \
        systemd.setenv=EOS_PLATFORM=ceoslab \
        systemd.setenv=container=docker \
        systemd.setenv=ETBA=1 \
        systemd.setenv=SKIP_ZEROTOUCH_BARRIER_IN_SYSDBINIT=1 \
        systemd.setenv=INTFTYPE=eth \
        systemd.setenv=MAPETH0=1 \
        systemd.setenv=MGMT_INTF=eth0
}

# wait_arista_ceos waits for cEOS to fully initialize (up to 180s).
# Health check: Cli -p 15 -c "show version" succeeds when all agents are ready.
wait_arista_ceos() {
    local cname="$1"
    info "waiting for Arista cEOS to initialize..."
    local deadline=$((SECONDS + 180))
    while [ "${SECONDS}" -lt "${deadline}" ]; do
        if podman exec "${cname}" Cli -p 15 -c "show version" &>/dev/null; then
            pass "Arista cEOS initialized"
            return 0
        fi
        sleep 5
    done
    fail "Arista cEOS did not initialize within 180s"
    return 1
}

# configure_nokia_srl waits for SR Linux to boot and applies BFD+BGP config.
# Must be called AFTER the veth link is injected into the container namespace
# so that SR Linux can discover ethernet-1/1.
configure_nokia_srl() {
    local cname="$1"

    # Wait for SR Linux management to be ready.
    info "waiting for SR Linux to boot (up to 180s)..."
    local max_wait=180
    local waited=0
    while [ "${waited}" -lt "${max_wait}" ]; do
        if podman exec "${cname}" sr_cli -d "show version" &>/dev/null; then
            break
        fi
        sleep 5
        waited=$((waited + 5))
    done

    if [ "${waited}" -ge "${max_wait}" ]; then
        fail "SR Linux did not become ready in ${max_wait}s"
        return 1
    fi

    pass "SR Linux booted in ${waited}s"

    # Wait for yang models to load before applying config.
    # sr_cli "show version" can succeed before yang loading is complete.
    info "waiting for SR Linux yang models to load..."
    local yang_wait=0
    while [ "${yang_wait}" -lt 60 ]; do
        # Try a simple config operation as a canary.
        if podman exec "${cname}" sr_cli -d "info / interface" &>/dev/null; then
            local result
            result="$(podman exec "${cname}" sr_cli -d "info / interface" 2>&1)"
            if ! echo "${result}" | grep -q "yang reload"; then
                break
            fi
        fi
        sleep 5
        yang_wait=$((yang_wait + 5))
    done

    # Give SR Linux a moment to discover the new e1-1 interface.
    sleep 5

    # Apply startup configuration.
    # SR Linux sr_cli handles set commands in a transaction automatically:
    # pipe all commands followed by "commit now" to apply atomically.
    info "applying SR Linux BFD+BGP config"
    podman cp "${SCRIPT_DIR}/nokia/config.cli" "${cname}:/tmp/config.cli"

    local config_applied=false
    local config_retries=0
    while [ "${config_retries}" -lt 6 ]; do
        local commit_output
        commit_output="$( (echo "enter candidate"; grep -v '^$' "${SCRIPT_DIR}/nokia/config.cli"; echo "commit now") | podman exec -i "${cname}" sr_cli 2>&1 )"
        if echo "${commit_output}" | grep -q "All changes have been committed"; then
            config_applied=true
            break
        fi
        info "config commit attempt $((config_retries + 1)) failed, retrying in 10s..."
        info "  output: $(echo "${commit_output}" | head -3)"
        sleep 10
        config_retries=$((config_retries + 1))
    done

    if [ "${config_applied}" = true ]; then
        pass "SR Linux configured"
    else
        fail "SR Linux config failed after ${config_retries} attempts"
        return 1
    fi

    # Bounce BFD subinterface to force timer renegotiation.
    # Nokia SR Linux YANG default for desired-minimum-transmit-interval is
    # 1000000μs (1s). When the BFD session establishes before config is committed,
    # it uses the default. Disabling and re-enabling the BFD subinterface forces
    # Nokia to renegotiate with the configured 300000μs (300ms) timers.
    info "bouncing SR Linux BFD subinterface for timer renegotiation"
    (
        echo "enter candidate"
        echo "set / bfd subinterface ethernet-1/1.0 admin-state disable"
        echo "commit now"
    ) | podman exec -i "${cname}" sr_cli 2>&1 | tail -3

    sleep 3

    (
        echo "enter candidate"
        echo "set / bfd subinterface ethernet-1/1.0 admin-state enable"
        echo "commit now"
    ) | podman exec -i "${cname}" sr_cli 2>&1 | tail -3

    # Verify BFD timers are at 300000μs.
    local timer_check
    timer_check="$( (echo "info from state /bfd subinterface ethernet-1/1.0") | podman exec -i "${cname}" sr_cli 2>&1 )"
    if echo "${timer_check}" | grep -q "desired-minimum-transmit-interval 300000"; then
        pass "SR Linux BFD timer set to 300000μs (300ms)"
    else
        info "SR Linux BFD timer state (may renegotiate after session Up):"
        echo "${timer_check}" | grep -E "(desired-minimum|required-minimum|detection-multiplier|active)" | head -6
    fi
}

# start_frr starts FRRouting container with BFD+BGP config.
# FRR uses config files mounted at startup — no post-boot configuration needed.
# FRR daemons require SYS_ADMIN for cap_set_proc during privilege dropping.
start_frr() {
    local cname="$1" image="$2"

    info "starting FRRouting: ${cname}"
    podman run -d \
        --name "${cname}" \
        --cap-add NET_ADMIN \
        --cap-add NET_RAW \
        --cap-add SYS_ADMIN \
        --user 0:0 \
        -v "${SCRIPT_DIR}/frr/daemons:/etc/frr/daemons:ro,z" \
        -v "${SCRIPT_DIR}/frr/frr.conf:/etc/frr/frr.conf:ro,z" \
        "${image}"
}

deploy_vendors() {
    local deployed=0

    for vdef in "${VENDOR_DEFS[@]}"; do
        local name cname image eth_idx peer_ip local_ip
        name="$(vendor_field "${vdef}" 1)"
        cname="$(vendor_field "${vdef}" 2)"
        image="$(vendor_field "${vdef}" 3):$(vendor_field "${vdef}" 4)"
        eth_idx="$(vendor_field "${vdef}" 5)"
        peer_ip="$(vendor_field "${vdef}" 6)"
        local_ip="$(vendor_field "${vdef}" 7)"

        # Check if image exists.
        if ! podman image exists "${image}" 2>/dev/null; then
            info "vendor ${name}: image ${image} not found — skipping"
            continue
        fi

        info "deploying vendor ${name} (${image})"

        # Start vendor container based on type.
        # Nokia SR Linux uses start_nokia_srl which handles boot wait + config.
        case "${name}" in
            nokia)
                start_nokia_srl "${cname}" "${image}"
                ;;
            arista)
                start_arista_ceos "${cname}" "${image}"
                ;;
            cisco)
                podman run -d --name "${cname}" --privileged \
                    -v "${SCRIPT_DIR}/cisco/xrd.cfg:/etc/xrd/startup.cfg:ro,z" \
                    "${image}"
                ;;
            sonic)
                podman run -d --name "${cname}" --privileged "${image}"
                ;;
            vyos)
                podman run -d --name "${cname}" --privileged \
                    -v "${SCRIPT_DIR}/vyos/config.boot:/opt/vyatta/etc/config/config.boot:ro,z" \
                    "${image}"
                ;;
            frr)
                start_frr "${cname}" "${image}"
                ;;
        esac

        # Verify vendor container is running.
        sleep 3
        if ! podman ps --format '{{.Names}}' | grep -q "${cname}"; then
            fail "vendor ${name} container failed to start"
            podman logs "${cname}" 2>&1 | tail -10 || true
            continue
        fi

        # Create veth link between GoBFD and vendor.
        local gobfd_pid vendor_pid
        gobfd_pid="$(get_container_pid "${GOBFD_CONTAINER}")"
        vendor_pid="$(get_container_pid "${cname}")"

        # Determine vendor-side interface name.
        local vendor_if
        case "${name}" in
            nokia)  vendor_if="e1-1" ;;
            arista) vendor_if="eth1" ;;
            cisco)  vendor_if="Gi0-0-0-0" ;;
            sonic)  vendor_if="eth1" ;;
            vyos)   vendor_if="eth1" ;;
            frr)    vendor_if="eth1" ;;
        esac

        create_veth_link "eth${eth_idx}" "${vendor_if}" \
            "${gobfd_pid}" "${vendor_pid}" \
            "${local_ip}/30" "${peer_ip}/30"

        # Configure IP on vendor side (for vendors that need it via netns).
        nsenter -t "${vendor_pid}" -n ip addr add "${peer_ip}/30" dev "${vendor_if}" 2>/dev/null || true

        # Add IPv6 addresses if defined for this vendor (dual-stack on same link).
        local v6_local="${VENDOR_IPV6_LOCAL[${name}]:-}"
        local v6_peer="${VENDOR_IPV6_PEER[${name}]:-}"
        if [ -n "${v6_local}" ] && [ -n "${v6_peer}" ]; then
            nsenter -t "${gobfd_pid}" -n ip -6 addr add "${v6_local}/127" dev "eth${eth_idx}" 2>/dev/null || true
            nsenter -t "${vendor_pid}" -n ip -6 addr add "${v6_peer}/127" dev "${vendor_if}" 2>/dev/null || true
        fi

        # Vendor-specific post-link configuration.
        case "${name}" in
            nokia)
                configure_nokia_srl "${cname}"
                ;;
            arista)
                wait_arista_ceos "${cname}"
                ;;
        esac

        pass "vendor ${name} deployed with link eth${eth_idx} <-> ${vendor_if}"
        DEPLOYED_VENDORS+=("${vdef}")
        deployed=$((deployed + 1))
    done

    if [ "${deployed}" -eq 0 ]; then
        fail "no vendor containers deployed"
        exit 1
    fi

    info "${deployed} vendor(s) deployed"
}

# ---------------------------------------------------------------------------
# Phase 4: Generate GoBFD config, start daemon + post-deploy config
# ---------------------------------------------------------------------------

# generate_configs creates gobfd.yml and gobgp.toml dynamically, containing
# only sessions/neighbors for actually deployed vendors. GoBFD fails to start
# if a configured local address has no corresponding interface.
#
# Vendor NOS like Nokia SR Linux require an established BGP session before
# creating BFD sessions. GoBGP runs alongside GoBFD to provide BGP peering
# (ASN 65001), and GoBFD propagates BFD state changes to GoBGP via gRPC
# (RFC 5882 Section 4.3).
generate_configs() {
    local gobfd_config="${SCRIPT_DIR}/gobfd/gobfd.generated.yml"
    local gobgp_config="${SCRIPT_DIR}/gobfd/gobgp.generated.toml"

    # --- GoBFD config ---
    cat > "${gobfd_config}" <<'HEADER'
grpc:
  addr: ":50052"

metrics:
  addr: ":9100"
  path: "/metrics"

log:
  level: "debug"
  format: "text"

bfd:
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3

gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "disable-peer"
  dampening:
    enabled: false

sessions:
HEADER

    for vdef in "${DEPLOYED_VENDORS[@]}"; do
        local name peer_ip local_ip
        name="$(vendor_field "${vdef}" 1)"
        peer_ip="$(vendor_field "${vdef}" 6)"
        local_ip="$(vendor_field "${vdef}" 7)"

        cat >> "${gobfd_config}" <<EOF
  # ${name} (IPv4)
  - peer: "${peer_ip}"
    local: "${local_ip}"
    type: single_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
EOF

        # Add IPv6 session if defined for this vendor.
        local v6_peer="${VENDOR_IPV6_PEER[${name}]:-}"
        local v6_local="${VENDOR_IPV6_LOCAL[${name}]:-}"
        if [ -n "${v6_peer}" ] && [ -n "${v6_local}" ]; then
            cat >> "${gobfd_config}" <<EOF
  # ${name} (IPv6)
  - peer: "${v6_peer}"
    local: "${v6_local}"
    type: single_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
EOF
        fi
    done

    # --- GoBGP config ---
    cat > "${gobgp_config}" <<'HEADER'
# Auto-generated GoBGP config for vendor BFD interop tests.
# ASN 65001, used alongside GoBFD for protocol-triggered BFD sessions.

[global.config]
as = 65001
router-id = "10.0.0.1"
port = 179

[global.apply-policy.config]
default-import-policy = "accept-route"
default-export-policy = "accept-route"
HEADER

    for vdef in "${DEPLOYED_VENDORS[@]}"; do
        local name peer_ip local_ip asn
        name="$(vendor_field "${vdef}" 1)"
        peer_ip="$(vendor_field "${vdef}" 6)"
        local_ip="$(vendor_field "${vdef}" 7)"
        asn="$(vendor_field "${vdef}" 8)"

        cat >> "${gobgp_config}" <<EOF

# --- ${name} (ASN ${asn}, IPv4) ---

[[neighbors]]
[neighbors.config]
neighbor-address = "${peer_ip}"
peer-as = ${asn}

[neighbors.transport.config]
local-address = "${local_ip}"

[[neighbors.afi-safis]]
[neighbors.afi-safis.config]
afi-safi-name = "ipv4-unicast"
EOF

        # Add IPv6 BGP neighbor if defined for this vendor.
        local v6_peer="${VENDOR_IPV6_PEER[${name}]:-}"
        local v6_local="${VENDOR_IPV6_LOCAL[${name}]:-}"
        if [ -n "${v6_peer}" ] && [ -n "${v6_local}" ]; then
            cat >> "${gobgp_config}" <<EOF

# --- ${name} (ASN ${asn}, IPv6) ---

[[neighbors]]
[neighbors.config]
neighbor-address = "${v6_peer}"
peer-as = ${asn}

[neighbors.transport.config]
local-address = "${v6_local}"

[[neighbors.afi-safis]]
[neighbors.afi-safis.config]
afi-safi-name = "ipv6-unicast"
EOF
        fi
    done

    info "generated configs: gobfd (${#DEPLOYED_VENDORS[@]} sessions) + gobgp (${#DEPLOYED_VENDORS[@]} neighbors)"

    # Copy configs into the container.
    podman exec "${GOBFD_CONTAINER}" mkdir -p /etc/gobfd /etc/gobgp
    podman cp "${gobfd_config}" "${GOBFD_CONTAINER}:/etc/gobfd/gobfd.yml"
    podman cp "${gobgp_config}" "${GOBFD_CONTAINER}:/etc/gobgp/gobgp.toml"
}

start_gobfd_daemon() {
    # Generate configs with only deployed vendor sessions.
    generate_configs

    info "starting gobfd + gobgp daemons inside container"

    # Show interface state for debugging.
    podman exec "${GOBFD_CONTAINER}" ip -br addr show 2>&1 || true

    # Start GoBGP first (BFD needs BGP sessions for protocol-triggered vendors).
    podman exec -d "${GOBFD_CONTAINER}" \
        gobgpd -f /etc/gobgp/gobgp.toml -l info
    sleep 2

    if podman exec "${GOBFD_CONTAINER}" pgrep -f "gobgpd" &>/dev/null; then
        pass "gobgpd daemon running"
    else
        fail "gobgpd daemon failed to start"
        exit 1
    fi

    # Start GoBFD (connects to GoBGP at localhost:50051).
    podman exec -d "${GOBFD_CONTAINER}" \
        /bin/gobfd -config /etc/gobfd/gobfd.yml
    sleep 3

    if podman exec "${GOBFD_CONTAINER}" pgrep -f "/bin/gobfd" &>/dev/null; then
        pass "gobfd daemon running"
    else
        fail "gobfd daemon failed to start"
        exit 1
    fi
}

configure_sonic() {
    local sonic_container="clab-${LAB_NAME}-sonic"

    if ! podman ps --format '{{.Names}}' | grep -q "${sonic_container}"; then
        return 0
    fi

    info "configuring SONiC-VS (post-deploy)"

    local max_wait=120
    local waited=0
    while [ "${waited}" -lt "${max_wait}" ]; do
        if podman exec "${sonic_container}" vtysh -c "show version" &>/dev/null; then
            break
        fi
        sleep 5
        waited=$((waited + 5))
    done

    if [ "${waited}" -ge "${max_wait}" ]; then
        info "SONiC FRR not ready after ${max_wait}s — skipping"
        return 0
    fi

    podman cp "${SCRIPT_DIR}/sonic/configure.sh" "${sonic_container}:/tmp/configure.sh"
    podman exec "${sonic_container}" bash /tmp/configure.sh
    pass "SONiC-VS configured"
}

# ---------------------------------------------------------------------------
# Phase 5: Wait for BFD convergence
# ---------------------------------------------------------------------------

wait_bfd_convergence() {
    info "waiting for BFD sessions to establish (up to 120s)"

    local max_wait=120
    local waited=0
    local interval=10

    while [ "${waited}" -lt "${max_wait}" ]; do
        local sessions
        sessions="$(podman exec "${GOBFD_CONTAINER}" \
            gobfdctl --addr localhost:50052 session list 2>/dev/null || true)"

        if echo "${sessions}" | grep -qE "\bUp\b"; then
            local up_count
            up_count="$(echo "${sessions}" | grep -cE "\bUp\b" || true)"
            info "${up_count} BFD session(s) Up after ${waited}s"
            echo "${sessions}"
            return 0
        fi

        sleep "${interval}"
        waited=$((waited + interval))
    done

    info "no BFD sessions Up after ${max_wait}s — Go tests will handle per-vendor checks"
    # Show current session state for debugging.
    podman exec "${GOBFD_CONTAINER}" \
        gobfdctl --addr localhost:50052 session list 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Phase 6: Run Go tests
# ---------------------------------------------------------------------------

run_tests() {
    info "running Go vendor interop tests"

    go test -tags interop_clab -v -count=1 -timeout 600s ./test/interop-clab/
    local exit_code=$?

    if [ "${exit_code}" -eq 0 ]; then
        pass "all vendor interop tests passed"
    else
        fail "vendor interop tests failed (exit code: ${exit_code})"
    fi

    return "${exit_code}"
}

# ---------------------------------------------------------------------------
# Available vendor images summary
# ---------------------------------------------------------------------------

show_vendor_images() {
    info "checking vendor image availability:"

    for vdef in "${VENDOR_DEFS[@]}"; do
        local name image
        name="$(vendor_field "${vdef}" 1)"
        image="$(vendor_field "${vdef}" 3):$(vendor_field "${vdef}" 4)"

        if podman image exists "${image}" 2>/dev/null; then
            info "  ${name} (${image}): AVAILABLE"
        else
            info "  ${name} (${image}): not found"
        fi
    done
}

# ===========================================================================
# Main
# ===========================================================================

echo ""
echo "========================================="
echo "  GoBFD Vendor BFD Interop Tests"
echo "  Arista | Nokia | Cisco | SONiC | VyOS | FRR"
echo "========================================="
echo ""

if [ "${TEST_ONLY}" = true ]; then
    run_tests
    exit $?
fi

check_prerequisites
show_vendor_images

build_gobfd_image
start_gobfd_container
deploy_vendors
start_gobfd_daemon
configure_sonic
wait_bfd_convergence

if [ "${UP_ONLY}" = true ]; then
    info "topology deployed — use 'make interop-clab-test' to run tests"
    info "cleanup with: make interop-clab-down"
    trap - EXIT
    exit 0
fi

run_tests
exit $?

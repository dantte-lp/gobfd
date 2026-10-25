# GoBFD Makefile — all Go commands run inside Podman containers.
# NO local Go toolchain required.
#
# Usage:
#   make up          — start dev container
#   make build       — compile both binaries
#   make test        — run all tests with race detector
#   make lint        — run golangci-lint v2
#   make gopls-check — run gopls diagnostics
#   make proto-gen   — generate protobuf Go code
#   make proto-lint  — lint proto definitions
#   make all           — build + test + lint
#   make interop       — run interop tests (builds + tests + cleans up)
#   make interop-test  — run Go interop tests (stack must be running)
#   make interop-testcontainers — build, test, and clean a Go-owned interop stack
#   make interop-bgp-testcontainers — build, test, and clean a Go-owned BGP+BFD stack
#   make interop-up    — start interop test stack (4 peers)
#   make interop-down  — stop interop test stack
#   make integration   — alias for interop

INTEROP_PROJECT_NAME ?= gobfd-interop
override INTEROP_PROJECT_NAME := $(value INTEROP_PROJECT_NAME)
_INTEROP_PROJECT_NAME_RAW := $(value INTEROP_PROJECT_NAME)
ifneq ($(findstring $$,$(_INTEROP_PROJECT_NAME_RAW)),)
$(error invalid INTEROP_PROJECT_NAME: Make function syntax is forbidden)
endif
export INTEROP_PROJECT_NAME
INTEROP_BGP_PROJECT_NAME ?=
override INTEROP_BGP_PROJECT_NAME := $(value INTEROP_BGP_PROJECT_NAME)
_INTEROP_BGP_PROJECT_NAME_RAW := $(value INTEROP_BGP_PROJECT_NAME)
ifneq ($(findstring $$,$(_INTEROP_BGP_PROJECT_NAME_RAW)),)
$(error invalid INTEROP_BGP_PROJECT_NAME: Make function syntax is forbidden)
endif
export INTEROP_BGP_PROJECT_NAME
PODMAN_COMPOSE_PROVIDER ?= docker-compose
PODMAN_COMPOSE_WARNING_LOGS ?= false
DOCKER_BUILDKIT ?= 0
export PODMAN_COMPOSE_PROVIDER PODMAN_COMPOSE_WARNING_LOGS DOCKER_BUILDKIT
COMPOSE := podman compose
PROJECT_SLUG := $(shell basename "$(CURDIR)" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$$//')
COMPOSE_PROJECT_NAME ?= $(PROJECT_SLUG)
COMPOSE_FILE := deployments/compose/compose.dev.yml
DC := COMPOSE_PROJECT_NAME=$(COMPOSE_PROJECT_NAME) $(COMPOSE) -p $(COMPOSE_PROJECT_NAME) -f $(COMPOSE_FILE)
EXEC := $(DC) exec -T dev
E2ECTL_BIN := /tmp/gobfd-e2ectl
SEMGREP ?= semgrep
SEMGREP_CONFIG ?= p/golang
SEMGREP_COMMON_FLAGS := --config $(SEMGREP_CONFIG) --metrics=off --disable-version-check --timeout 15 --no-git-ignore --include='*.go' .

.PHONY: all verify build test testcontainers-smoke lint lint-ci lint-fix gopls-check lint-docs lint-md lint-yaml lint-spell lint-commit semgrep semgrep-json semgrep-pro proto-gen proto-lint fuzz vulncheck osv-scan vulncheck-strict osv-scan-strict dependency-inventory dependency-inventory-check \
        benchmark benchmark-all benchmark-save benchmark-compare \
        test-report report-all \
        coverage profile \
        up down restart logs clean tidy \
        dev-ps dev-project dev-ensure e2ectl-build \
        e2e-help e2e-core e2e-core-testcontainers \
        e2e-routing e2e-routing-test e2e-rfc e2e-rfc-test e2e-overlay e2e-overlay-test e2e-linux e2e-linux-test e2e-vendor e2e-vendor-test \
	interop-project-validate interop interop-test interop-testcontainers interop-up interop-down interop-logs \
        interop-capture interop-pcap interop-pcap-summary integration \
        interop-bgp interop-bgp-test interop-bgp-testcontainers interop-bgp-up interop-bgp-down interop-bgp-logs \
        interop-rfc interop-rfc-test interop-rfc-testcontainers interop-rfc-up interop-rfc-down interop-rfc-logs \
        interop-clab interop-clab-bootstrap interop-clab-test interop-clab-up interop-clab-down \
        int-bgp-failover int-bgp-failover-testcontainers int-bgp-failover-up int-bgp-failover-down int-bgp-failover-logs \
        int-haproxy int-haproxy-testcontainers int-haproxy-up int-haproxy-down int-haproxy-logs \
        int-observability int-observability-testcontainers int-observability-up int-observability-down int-observability-logs \
        int-exabgp-anycast int-exabgp-anycast-up int-exabgp-anycast-down int-exabgp-anycast-logs \
        int-k8s int-k8s-up int-k8s-down \
        benchmark-cross benchmark-report

# === Lifecycle ===

up:
	$(DC) up -d --build

down:
	$(DC) down

restart: down up

logs:
	$(DC) logs -f dev

dev-ps:
	$(DC) ps

dev-project:
	@echo "$(COMPOSE_PROJECT_NAME)"

# dev-ensure guards that the gobfd dev container exists and points at the
# current working directory. If the bind-mount source no longer matches
# $(CURDIR) (typical after worktree teardown) the container is force-recreated.
# This is the canonical pre-flight for build / test / lint targets.
dev-ensure:
	@set -e; \
	cid="$$( $(DC) ps --all --quiet dev )"; \
	if [ -z "$$cid" ]; then \
	  echo "dev-ensure: dev service container missing, creating"; \
	  if podman image exists "$(COMPOSE_PROJECT_NAME)-dev:latest"; then \
	    $(DC) up -d --no-build dev; \
	  else \
	    $(DC) up -d --build dev; \
	  fi; \
	  exit 0; \
	fi; \
	src="$$(podman inspect "$$cid" --format '{{range .Mounts}}{{if eq .Destination "/app"}}{{.Source}}{{end}}{{end}}')"; \
	if [ "$$src" != "$(CURDIR)" ]; then \
	  echo "dev-ensure: mount source $$src != $(CURDIR), force recreating"; \
	  $(DC) up -d --no-build --force-recreate dev; \
	  exit 0; \
	fi; \
	$(DC) up -d --no-build dev

e2ectl-build: dev-ensure
	$(EXEC) go build -trimpath -o $(E2ECTL_BIN) ./test/cmd/e2ectl

# === S10 Extended E2E Targets ===

e2e-help:
	@printf '%s\n' \
		'S10 E2E targets' \
		'  e2e-core      implemented: GoBFD daemon-to-daemon scenarios' \
		'  e2e-routing   implemented: FRR/BIRD3/GoBGP/ExaBGP aggregate' \
		'  e2e-rfc       implemented: RFC 7419/9384/9468/9747 aggregate' \
		'  e2e-overlay   implemented: VXLAN/Geneve backend boundary checks' \
		'  e2e-linux     implemented: rtnetlink/kernel-bond/OVSDB/NM ownership checks' \
		'  e2e-vendor    implemented: optional containerlab vendor profile evidence'

e2e-core: e2e-core-testcontainers

e2e-core-testcontainers: e2ectl-build
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_core_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/core/...
	$(EXEC) $(E2ECTL_BIN) core
e2e-routing: interop-project-validate
	$(DC) up -d --build --force-recreate dev
	go run ./test/cmd/e2ectl routing

e2e-routing-test: interop-project-validate
	$(INTEROP_CTL) lock-run -- env "COMPOSE_PROJECT_NAME=$(COMPOSE_PROJECT_NAME)" \
		$(INTEROP_CTL) dev-exec -- \
		env "INTEROP_PROJECT_NAME=$${INTEROP_PROJECT_NAME}" \
		INTEROP_COMPOSE_FILE=/app/test/interop/compose.yml \
		go test -tags interop -v -count=1 -timeout 300s ./test/interop/
	@bgp_project="$${INTEROP_PROJECT_NAME}-bgp"; \
	INTEROP_PROJECT_NAME="$${bgp_project}" INTEROP_PROJECT_KIND=bgp \
		$(INTEROP_CTL) lock-run -- env "COMPOSE_PROJECT_NAME=$(COMPOSE_PROJECT_NAME)" \
		$(INTEROP_CTL) dev-exec -- \
		env "INTEROP_PROJECT_NAME=$${bgp_project}" \
		INTEROP_BGP_COMPOSE_FILE=/app/test/interop-bgp/compose.yml \
		go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/

e2e-rfc:
	$(DC) up -d --build --force-recreate dev
	go run ./test/cmd/e2ectl rfc

e2e-rfc-test:
	$(EXEC) env INTEROP_RFC_COMPOSE_FILE=/app/test/interop-rfc/compose.yml \
		go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/

e2e-overlay:
	$(DC) up -d --build --force-recreate dev
	go run ./test/cmd/e2ectl overlay

e2e-overlay-test: e2e-overlay

e2e-linux:
	$(DC) up -d --build --force-recreate dev
	go run ./test/cmd/e2ectl linux

e2e-linux-test: e2e-linux

e2e-vendor:
	$(DC) up -d --build --force-recreate dev
	go run ./test/cmd/e2ectl vendor

e2e-vendor-test:
	$(EXEC) go test -tags e2e_vendor -v -count=1 -timeout 120s ./test/e2e/vendor/

# === Build ===

all: build test gopls-check lint lint-docs

# Canonical pre-commit gate for routine changes. Protocol or wire-format
# changes must add the relevant interop target on top of this gate.
verify: all proto-lint vulncheck
	@echo "verify: build, test, lint, docs, proto, and vulnerability gates passed"

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BENCH_DIR    := testdata/benchmarks
REPORT_DIR   := reports
BENCH_VERSION ?= $(VERSION)
LDFLAGS := -s -w \
  -X github.com/dantte-lp/gobfd/internal/version.Version=$(VERSION) \
  -X github.com/dantte-lp/gobfd/internal/version.GitCommit=$(GIT_COMMIT) \
  -X github.com/dantte-lp/gobfd/internal/version.BuildDate=$(BUILD_DATE)

build: dev-ensure
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfd
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfdctl
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfd-haproxy-agent
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfd-exabgp-bridge

# === Test ===

test: dev-ensure
	$(EXEC) go test ./... -race -count=1

test-v: dev-ensure
	$(EXEC) go test ./... -race -count=1 -v

testcontainers-smoke: dev-ensure
	$(EXEC) env DOCKER_HOST=unix:///run/podman/podman.sock \
		GOBFD_REQUIRE_PODMAN=1 \
		go test -tags testcontainers ./test/internal/containertest -race -count=1 -v

test-run:
	@test -n "$(RUN)" || (echo "Usage: make test-run RUN=TestFSMTransition PKG=./internal/bfd"; exit 1)
	$(EXEC) go test -run '$(RUN)' $(PKG) -race -count=1 -v

fuzz:
	@test -n "$(FUNC)" || (echo "Usage: make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd"; exit 1)
	$(EXEC) go test -fuzz=$(FUNC) $(PKG) -fuzztime=60s

test-integration:
	$(EXEC) go test -tags integration ./test/integration/ -race -count=1 -v

# === Interop Tests (FRR 10.7.1 + BIRD 3.4.0+fix + Holo 0.9.0 + Thoro/bfd — 4-peer topology) ===

INTEROP_COMPOSE := test/interop/compose.yml
INTEROP_CTL := go run ./test/cmd/interopctl

interop-project-validate:
	@case "$${INTEROP_PROJECT_NAME}" in \
	  ''|[!a-z0-9]*|*[!a-z0-9_-]*) \
	    printf 'invalid INTEROP_PROJECT_NAME %s: use lowercase letters, digits, dashes, and underscores\n' \
	      "$${INTEROP_PROJECT_NAME}" >&2; \
	    exit 2 ;; \
	esac

interop: interop-testcontainers

interop-test: interop-project-validate
	$(INTEROP_CTL) lock-run -- env "COMPOSE_PROJECT_NAME=$(COMPOSE_PROJECT_NAME)" \
		$(INTEROP_CTL) dev-exec -- \
		env "INTEROP_PROJECT_NAME=$${INTEROP_PROJECT_NAME}" \
		INTEROP_COMPOSE_FILE=$(INTEROP_COMPOSE) \
		go test -tags interop -v -count=1 -timeout 300s ./test/interop/

interop-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run \
		--build-tags interop_testcontainers ./test/interop/...
	$(EXEC) go test -tags interop_testcontainers -race -count=1 -run '^(TestTimerReload.+|TestCraftedPeerEvidence.+)$$' ./test/interop/
	$(EXEC) env DOCKER_HOST=unix:///run/podman/podman.sock \
		GOBFD_REQUIRE_PODMAN=1 \
		INTEROP_TESTCONTAINERS_ARTIFACT_DIR=/app/reports/e2e/interop-testcontainers \
		GOBFD_BUILD_REVISION="$(shell git rev-parse --verify HEAD^{commit})" \
		go test -tags interop_testcontainers -race -count=1 -v -timeout 20m \
		-run '^(TestFourPeerTopologyTestcontainers|TestFourPeerMeticulousTopologyTestcontainers|TestBIRDFlowSpecValidation|TestCraftedPeerTestcontainers|TestCraftedMultihopTTLTestcontainers)$$' ./test/interop/

interop-up: interop-project-validate
	$(INTEROP_CTL) up

interop-down: interop-project-validate
	$(INTEROP_CTL) down

interop-logs: interop-project-validate
	$(INTEROP_CTL) logs

interop-capture: interop-project-validate
	$(INTEROP_CTL) capture

interop-pcap: interop-project-validate
	$(INTEROP_CTL) pcap

interop-pcap-summary: interop-project-validate
	$(INTEROP_CTL) summary

integration: interop

# === BGP+BFD Interop Tests (GoBGP + FRR + BIRD3 + ExaBGP — 3 scenarios) ===

INTEROP_BGP_COMPOSE := test/interop-bgp/compose.yml
INTEROP_BGP_CTL := env "INTEROP_PROJECT_NAME=$${INTEROP_BGP_PROJECT_NAME:-$${INTEROP_PROJECT_NAME}-bgp}" INTEROP_PROJECT_KIND=bgp $(INTEROP_CTL)

interop-bgp: interop-bgp-testcontainers

interop-bgp-test: interop-project-validate
	$(INTEROP_BGP_CTL) lock-run -- env "COMPOSE_PROJECT_NAME=$(COMPOSE_PROJECT_NAME)" \
		$(INTEROP_CTL) dev-exec -- \
		env "INTEROP_PROJECT_NAME=$${INTEROP_BGP_PROJECT_NAME:-$${INTEROP_PROJECT_NAME}-bgp}" \
		INTEROP_BGP_COMPOSE_FILE=$(INTEROP_BGP_COMPOSE) \
		go test -tags interop_bgp -race -v -count=1 -timeout 300s ./test/interop-bgp/

interop-bgp-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run \
		--build-tags interop_bgp_testcontainers ./test/interop-bgp/...
	$(EXEC) env DOCKER_HOST=unix:///run/podman/podman.sock \
		GOBFD_REQUIRE_PODMAN=1 \
		INTEROP_BGP_TESTCONTAINERS_ARTIFACT_DIR=/app/reports/e2e/interop-bgp-testcontainers \
		GOBFD_BUILD_REVISION="$(shell git rev-parse --verify HEAD^{commit})" \
		go test -tags interop_bgp_testcontainers -race -count=1 -v -timeout 15m \
		-run '^TestBGPBFDTopologyTestcontainers$$' ./test/interop-bgp/

interop-bgp-up: interop-project-validate
	$(INTEROP_BGP_CTL) up

interop-bgp-down: interop-project-validate
	$(INTEROP_BGP_CTL) down

interop-bgp-logs: interop-project-validate
	$(INTEROP_BGP_CTL) logs

# === RFC Interop Tests (RFC 7419 + RFC 9384 + RFC 9468 + RFC 9747) ===

INTEROP_RFC_COMPOSE := test/interop-rfc/compose.yml
INTEROP_RFC_PROJECT ?= gobfd-interop-rfc
INTEROP_RFC_DC := $(COMPOSE) -p $(INTEROP_RFC_PROJECT) -f $(INTEROP_RFC_COMPOSE)

interop-rfc: interop-rfc-testcontainers

interop-rfc-test:
	$(EXEC) env INTEROP_PROJECT_NAME=$(INTEROP_RFC_PROJECT) \
		INTEROP_RFC_COMPOSE_FILE=$(INTEROP_RFC_COMPOSE) \
		go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/

interop-rfc-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run \
		--build-tags interop_rfc_testcontainers ./test/interop-rfc/...
	$(EXEC) env DOCKER_HOST=unix:///run/podman/podman.sock \
		GOBFD_REQUIRE_PODMAN=1 \
		INTEROP_RFC_TESTCONTAINERS_ARTIFACT_DIR=/app/reports/e2e/interop-rfc-testcontainers \
		GOBFD_BUILD_REVISION="$(shell git rev-parse --verify HEAD^{commit})" \
		go test -tags interop_rfc_testcontainers -race -count=1 -v -timeout 15m \
		-run '^TestRFCInteropTopologyTestcontainers$$' ./test/interop-rfc/

interop-rfc-up:
	$(INTEROP_RFC_DC) up --build -d

interop-rfc-down:
	$(INTEROP_RFC_DC) down --volumes --remove-orphans

interop-rfc-logs:
	$(INTEROP_RFC_DC) logs -f

# === Containerlab Vendor Interop Tests (Arista + Nokia + Cisco + SONiC + VyOS) ===

CLAB_TOPO := test/interop-clab/gobfd-vendors.clab.yml

# Cold-start boundary only: the controller and payload lifecycle remain Go-owned.
# All effects stay in the recipe, so make -n does not create launcher resources.
# The lock inode persists; its descriptor is inherited by compiler/controller children.
define CLAB_COLD_CONTROLLER
@set -eu; \
umask 077; LC_ALL=C; export LC_ALL; \
clab_root=$$(pwd -P); clab_uid=$$(id -u); \
clab_runtime="$$clab_root/test/interop-clab/gobfd/.runtime"; \
clab_fail() { printf '%s\n' "clabbootstrap: $$*" >&2; return 1; }; \
clab_private_dir() { \
  [ ! -L "$$1" ] && [ -d "$$1" ] && \
  [ "$$(stat -c '%a:%u:%F' -- "$$1")" = "700:$$clab_uid:directory" ]; \
}; \
clab_private_file() { \
  [ ! -L "$$1" ] && [ -f "$$1" ] && \
  [ "$$(stat -c '%a:%u:%h' -- "$$1")" = "600:$$clab_uid:1" ]; \
}; \
clab_hex() { \
  [ "$${#1}" -eq "$$2" ] || return 1; \
  case "$$1" in *[!0123456789abcdef]*) return 1;; esac; \
}; \
mkdir -p -- "$$clab_runtime"; \
clab_private_dir "$$clab_runtime" || { clab_fail 'runtime directory must be owned and private'; exit 1; }; \
clab_lock="$$clab_runtime/controller-build.lock"; \
if [ ! -e "$$clab_lock" ] && [ ! -L "$$clab_lock" ]; then \
  (set -C; : > "$$clab_lock") || exit 1; \
fi; \
clab_private_file "$$clab_lock" || { clab_fail 'unsafe bootstrap lock'; exit 1; }; \
exec 9<>"$$clab_lock"; \
flock -n 9 || { clab_fail 'another cold controller holds the bootstrap lock'; exit 1; }; \
[ "$$(stat -Lc '%d:%i' -- /proc/$$$$/fd/9)" = "$$(stat -c '%d:%i' -- "$$clab_lock")" ] || exit 1; \
clab_intent="$$clab_runtime/controller-build.intent"; clab_child=; \
clab_run() { \
  "$$@" & clab_child=$$!; \
  if wait "$$clab_child"; then clab_result=0; else clab_result=$$?; fi; \
  clab_child=; return "$$clab_result"; \
}; \
clab_exists() { timeout --kill-after=5s 30s podman container exists "$$1"; }; \
clab_identity() { \
  clab_record=$$(timeout --kill-after=5s 30s podman inspect --type container --format \
    '{{.Id}}|{{index .Config.Labels "io.gobfd.interop-clab.owner"}}|{{index .Config.Labels "io.gobfd.interop-clab.run"}}' \
    "$$clab_name") || return $$?; \
  clab_observed=$${clab_record%%|*}; clab_labels=$${clab_record#*|}; \
  clab_owner=$${clab_labels%%|*}; clab_run_id=$${clab_labels#*|}; \
  clab_hex "$$clab_observed" 64 && [ "$$clab_owner" = gobfd-vendors ] && \
    [ "$$clab_run_id" = "$$clab_nonce" ] && \
    [ "$$clab_record" = "$$clab_observed|$$clab_owner|$$clab_run_id" ] || \
    { clab_fail 'compiler identity is foreign or malformed'; return 1; }; \
  printf '%s\n' "$$clab_observed"; \
}; \
clab_load_intent() { \
  clab_private_file "$$clab_intent" && [ "$$(stat -c '%s' -- "$$clab_intent")" = 33 ] || \
    { clab_fail 'unsafe bootstrap intent'; return 1; }; \
  clab_nonce=$$(cat -- "$$clab_intent") || return $$?; \
  clab_hex "$$clab_nonce" 32 && \
    [ "$$(printf '%s\n' "$$clab_nonce" | sha256sum)" = "$$(sha256sum < "$$clab_intent")" ] || \
    { clab_fail 'bootstrap intent must contain exactly one nonce'; return 1; }; \
  clab_base="controller-build-$$clab_nonce"; clab_context="$$clab_runtime/$$clab_base"; \
  clab_name="gobfd-clab-bootstrap-$$clab_nonce"; clab_cid="$$clab_context/compiler.cid"; \
  if [ -e "$$clab_context" ] || [ -L "$$clab_context" ]; then \
    clab_private_dir "$$clab_context" || { clab_fail 'unsafe compiler output directory'; return 1; }; \
  fi; \
}; \
clab_absent() { \
  if clab_exists "$$1"; then clab_fail 'compiler reference remains present'; return 1; \
  else clab_status=$$?; [ "$$clab_status" -eq 1 ] || return "$$clab_status"; fi; \
}; \
clab_remove_compiler() { \
  clab_expected=; clab_prefix=; clab_partial=0; \
  if [ -e "$$clab_cid" ] || [ -L "$$clab_cid" ]; then \
    clab_private_file "$$clab_cid" || return 1; \
    clab_cid_size=$$(stat -c '%s' -- "$$clab_cid") || return $$?; \
    [ "$$clab_cid_size" -le 65 ] || return 1; \
    clab_prefix=$$(cat -- "$$clab_cid") || return $$?; \
    if [ "$$clab_cid_size" -eq 65 ]; then \
      clab_hex "$$clab_prefix" 64 && \
        [ "$$(printf '%s\n' "$$clab_prefix" | sha256sum)" = "$$(sha256sum < "$$clab_cid")" ] || return 1; \
      clab_expected=$$clab_prefix; \
    else \
      [ "$${#clab_prefix}" -eq "$$clab_cid_size" ] || return 1; \
      case "$$clab_prefix" in *[!0123456789abcdef]*) return 1;; esac; \
      clab_partial=1; \
    fi; \
  fi; \
  if clab_exists "$$clab_name"; then \
    clab_seen=$$(clab_identity) || return $$?; \
    [ -z "$$clab_expected" ] || [ "$$clab_expected" = "$$clab_seen" ] || \
      { clab_fail 'compiler ID differs from its checkpoint'; return 1; }; \
    if [ -z "$$clab_expected" ]; then \
      clab_private_dir "$$clab_context" || return 1; \
      if [ "$$clab_partial" -eq 1 ]; then \
        case "$$clab_seen" in "$$clab_prefix"*) :;; \
          *) clab_fail 'partial compiler ID differs from its owner'; return 1;; esac; \
        printf '%s\n' "$${clab_seen#"$$clab_prefix"}" >> "$$clab_cid" || return $$?; \
      else \
        (set -C; printf '%s\n' "$$clab_seen" > "$$clab_cid") || return $$?; \
      fi; \
      sync -- "$$clab_cid" "$$clab_context" || return $$?; \
      clab_expected=$$clab_seen; \
    fi; \
    clab_current=$$(clab_identity) || return $$?; \
    [ "$$clab_current" = "$$clab_expected" ] || return 1; \
    timeout --kill-after=5s 60s podman rm --force "$$clab_expected" || return $$?; \
  else \
    clab_status=$$?; [ "$$clab_status" -eq 1 ] || return "$$clab_status"; \
    [ "$$clab_partial" -eq 0 ] || [ -z "$$clab_prefix" ] || \
      { clab_fail 'partial compiler checkpoint has no matching named container'; return 1; }; \
  fi; \
  clab_absent "$$clab_name" || return $$?; \
  if [ -n "$$clab_expected" ]; then clab_absent "$$clab_expected" || return $$?; fi; \
}; \
clab_cleanup() { \
  if [ ! -e "$$clab_intent" ] && [ ! -L "$$clab_intent" ]; then return 0; fi; \
  clab_private_dir "$$clab_runtime" && clab_load_intent && clab_remove_compiler || return $$?; \
  ( \
    cd -- "$$clab_runtime" || exit 1; \
    clab_private_dir . && \
      [ "$$(stat -c '%d:%i' .)" = "$$(stat -c '%d:%i' -- "$$clab_runtime")" ] || exit 1; \
    if [ -e "./$$clab_base" ] || [ -L "./$$clab_base" ]; then \
      clab_private_dir "./$$clab_base" && rm -rf --one-file-system -- "./$$clab_base" || exit 1; \
    fi; \
    [ ! -e "./$$clab_base" ] && [ ! -L "./$$clab_base" ] || exit 1; \
    sync -- . || exit 1; \
    clab_private_file controller-build.intent && rm -- controller-build.intent || exit 1; \
    sync -- .; \
  ); \
}; \
clab_finish() { \
  clab_exit=$$?; trap - 0 HUP INT TERM; \
  if clab_cleanup; then :; elif [ "$$clab_exit" -eq 0 ]; then clab_exit=1; fi; \
  exit "$$clab_exit"; \
}; \
clab_signal() { \
  trap - HUP INT TERM; \
  if [ -n "$$clab_child" ]; then kill -"$$1" "$$clab_child" 2>/dev/null || :; wait "$$clab_child" || :; fi; \
  exit "$$2"; \
}; \
trap 'clab_finish' 0; trap 'clab_signal HUP 129' HUP; \
trap 'clab_signal INT 130' INT; trap 'clab_signal TERM 143' TERM; \
clab_cleanup || exit $$?; \
clab_nonce=$$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n'); \
clab_hex "$$clab_nonce" 32 || exit 1; \
clab_base="controller-build-$$clab_nonce"; clab_context="$$clab_runtime/$$clab_base"; \
clab_name="gobfd-clab-bootstrap-$$clab_nonce"; \
[ ! -e "$$clab_context" ] && [ ! -L "$$clab_context" ] || exit 1; \
clab_absent "$$clab_name" || exit $$?; \
(set -C; printf '%s\n' "$$clab_nonce" > "$$clab_intent"); \
sync -- "$$clab_intent" "$$clab_runtime"; \
mkdir -- "$$clab_context" "$$clab_context/src" "$$clab_context/out"; \
clab_load_intent; \
clab_copy_source() { \
  [ ! -L "$$1" ] && [ -f "$$1" ] && [ "$$(stat -c '%s' -- "$$1")" -le 2097152 ] || return 1; \
  cp -- "$$1" "$$2" && chmod 0400 -- "$$2"; \
}; \
for clab_input in go.mod go.sum; do \
  clab_copy_source "$$clab_root/$$clab_input" "$$clab_context/src/$$clab_input"; \
done; \
for clab_package in test/cmd/clabbootstrap test/internal/clabbootstrap test/internal/interopproject \
  test/internal/podmanapi test/internal/vendorprofiles; do \
  [ ! -L "$$clab_root/$$clab_package" ] && [ -d "$$clab_root/$$clab_package" ] || exit 1; \
  mkdir -p -- "$$clab_context/src/$$clab_package"; \
  clab_count=0; \
  for clab_input in "$$clab_root/$$clab_package/"*.go; do \
    case "$$clab_input" in *_test.go) continue;; esac; \
    clab_file=$${clab_input##*/}; \
    clab_copy_source "$$clab_input" "$$clab_context/src/$$clab_package/$$clab_file"; \
    clab_count=$$((clab_count + 1)); \
  done; \
  [ "$$clab_count" -gt 0 ] || exit 1; \
done; \
case $$(uname -m) in x86_64) clab_arch=amd64;; aarch64|arm64) clab_arch=arm64;; *) exit 1;; esac; \
clab_created=$$(clab_run timeout --kill-after=5s 60s podman create \
  --name "$$clab_name" --network bridge --read-only --cap-drop ALL \
  --label io.gobfd.interop-clab.owner=gobfd-vendors --label "io.gobfd.interop-clab.run=$$clab_nonce" \
  --security-opt no-new-privileges --security-opt label=disable --image-volume ignore \
  --cpus 2 --memory 2g --memory-swap 2g --pids-limit 512 --pull never \
  --tmpfs /tmp:rw,nosuid,nodev,size=512m --tmpfs /root/.cache/go-build:rw,nosuid,nodev,size=512m \
  --tmpfs /go/pkg/mod:rw,nosuid,nodev,size=512m --workdir /src \
  --env GOTOOLCHAIN=local --env GOWORK=off --env CGO_ENABLED=0 --env GOOS=linux --env "GOARCH=$$clab_arch" \
  --env GOMAXPROCS=2 --env GOMEMLIMIT=512MiB \
  --mount "type=bind,src=$$clab_context/src,dst=/src,ro=true" \
  --mount "type=bind,src=$$clab_context/out,dst=/out" \
  docker.io/library/golang:1.27.3-trixie@sha256:604106102d33b1084e37ca08adf98ac0813624ae8595bd0d7f3965ecebded09c \
  /usr/bin/timeout --signal=TERM --kill-after=5s 900s /bin/sh -eu -c \
  '"$$@"; go version -m /out/clabbootstrap > /out/build-info; \
   grep -Fx "/out/clabbootstrap: go1.27.3" /out/build-info > /dev/null; \
   grep -Fx "$$(printf "\tpath\tgithub.com/dantte-lp/gobfd/test/cmd/clabbootstrap")" /out/build-info > /dev/null; \
   grep -Fx "$$(printf "\tbuild\tCGO_ENABLED=0")" /out/build-info > /dev/null; \
   grep -Fx "$$(printf "\tbuild\tGOOS=linux")" /out/build-info > /dev/null; \
   grep -Fx "$$(printf "\tbuild\tGOARCH=%s" "$$GOARCH")" /out/build-info > /dev/null' \
  clab-compile go build -trimpath -mod=readonly -buildvcs=false -o /out/clabbootstrap ./test/cmd/clabbootstrap); \
clab_hex "$$clab_created" 64 && [ "$$(clab_identity)" = "$$clab_created" ] || exit 1; \
(set -C; printf '%s\n' "$$clab_created" > "$$clab_cid"); \
sync -- "$$clab_cid" "$$clab_context"; \
if clab_run timeout --kill-after=5s 960s podman start --attach "$$clab_created"; then :; else exit $$?; fi; \
[ "$$(clab_identity)" = "$$clab_created" ] || exit 1; \
clab_state=$$(timeout --kill-after=5s 30s podman inspect --type container --format \
  '{{.State.Status}}|{{.State.Running}}|{{.State.ExitCode}}|{{.State.OOMKilled}}' "$$clab_created"); \
[ "$$clab_state" = 'exited|false|0|false' ] || \
  { clab_fail 'compiler did not finish successfully'; exit 1; }; \
clab_remove_compiler; \
clab_artifact="$$clab_context/out/clabbootstrap"; clab_native="$$clab_context/clabbootstrap"; \
[ ! -L "$$clab_artifact" ] && [ -f "$$clab_artifact" ] && [ -s "$$clab_artifact" ] && \
  [ "$$(stat -c '%s' -- "$$clab_artifact")" -le 134217728 ] || exit 1; \
clab_digest=$$(sha256sum < "$$clab_artifact"); \
(set -C; cat -- "$$clab_artifact" > "$$clab_native"); chmod 0500 -- "$$clab_native"; \
[ "$$(sha256sum < "$$clab_native")" = "$$clab_digest" ] || exit 1; \
if clab_run timeout --signal=TERM --kill-after=15s 1800s "$$clab_native" $(1); then exit 0; else exit $$?; fi
endef

interop-clab-bootstrap:
	$(call CLAB_COLD_CONTROLLER,$(ARGS))

interop-clab:
	$(call CLAB_COLD_CONTROLLER,--test)

interop-clab-test:
	$(call CLAB_COLD_CONTROLLER,--test-only)

interop-clab-up:
	$(call CLAB_COLD_CONTROLLER,--deploy)

interop-clab-down:
	$(call CLAB_COLD_CONTROLLER,--down)

# === Integration Examples ===

INT_BGP_CTL := env "INTEROP_PROJECT_NAME=$${INTEROP_PROJECT_NAME}-bgp-fast-failover" INTEROP_PROJECT_KIND=bgp-fast-failover $(INTEROP_CTL)
INT_HAPROXY_DC := $(COMPOSE) -f deployments/integrations/haproxy-health/compose.yml
INT_OBS_DC := $(COMPOSE) -f deployments/integrations/observability/compose.yml
INT_EXABGP_CTL := env "INTEROP_PROJECT_NAME=$${INTEROP_PROJECT_NAME}-exabgp-anycast" INTEROP_PROJECT_KIND=exabgp-anycast $(INTEROP_CTL)

int-bgp-failover: int-bgp-failover-testcontainers

int-bgp-failover-testcontainers: e2ectl-build
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_bgp_failover_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/bgp-failover/...
	$(EXEC) env GOBFD_BUILD_REVISION="$(shell git rev-parse --verify HEAD^{commit})" \
		$(E2ECTL_BIN) bgp-fast-failover

int-bgp-failover-up: interop-project-validate
	$(INT_BGP_CTL) up

int-bgp-failover-down: interop-project-validate
	$(INT_BGP_CTL) down

int-bgp-failover-logs: interop-project-validate
	$(INT_BGP_CTL) logs

int-haproxy:
	go run ./test/cmd/integrationctl haproxy

int-haproxy-testcontainers: e2ectl-build
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_haproxy_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/haproxy-health/...
	$(EXEC) $(E2ECTL_BIN) haproxy-health

int-haproxy-up:
	$(INT_HAPROXY_DC) up --build -d

int-haproxy-down:
	$(INT_HAPROXY_DC) down --volumes --remove-orphans

int-haproxy-logs:
	$(INT_HAPROXY_DC) logs -f

int-observability:
	go run ./test/cmd/integrationctl observability

int-observability-testcontainers: e2ectl-build
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_observability_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/observability/...
	$(EXEC) $(E2ECTL_BIN) observability

int-observability-up:
	$(INT_OBS_DC) up --build -d

int-observability-down:
	$(INT_OBS_DC) down --volumes --remove-orphans

int-observability-logs:
	$(INT_OBS_DC) logs -f

int-exabgp-anycast:
	go run ./test/cmd/integrationctl exabgp-anycast

int-exabgp-anycast-up: interop-project-validate
	$(INT_EXABGP_CTL) up

int-exabgp-anycast-down: interop-project-validate
	$(INT_EXABGP_CTL) down

int-exabgp-anycast-logs: interop-project-validate
	$(INT_EXABGP_CTL) logs

int-k8s:
	go run ./test/cmd/integrationctl k8s

int-k8s-up:
	go run ./test/cmd/integrationctl k8s-up

int-k8s-down:
	go run ./test/cmd/integrationctl k8s-down

# === Benchmarks & Profiling ===

benchmark:
	$(EXEC) go test -buildvcs=false -bench=. -benchmem -count=6 -run='^$$' ./internal/bfd/ | tee benchmark.txt

benchmark-all:
	$(EXEC) go test -buildvcs=false -bench=. -benchmem -count=6 -run='^$$' ./... | tee benchmark.txt

coverage:
	$(EXEC) go test -buildvcs=false ./... -race -count=1 -coverprofile=coverage.out -covermode=atomic
	$(EXEC) go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

coverage-func:
	$(EXEC) go test -buildvcs=false ./... -race -count=1 -coverprofile=coverage.out -covermode=atomic
	$(EXEC) go tool cover -func=coverage.out

profile:
	$(EXEC) go test -buildvcs=false -bench=. -run='^$$' -cpuprofile=cpu.prof -memprofile=mem.prof ./internal/bfd/
	@echo "CPU profile: cpu.prof — view with: go tool pprof -http=:8080 cpu.prof"
	@echo "Memory profile: mem.prof — view with: go tool pprof -http=:8080 mem.prof"

# === Reports ===

# Save benchmarks tagged by version.
# Usage: make benchmark-save [BENCH_VERSION=v0.5.0]
benchmark-save:
	@mkdir -p $(BENCH_DIR)/$(BENCH_VERSION)
	$(EXEC) go test -buildvcs=false -bench=. -benchmem -count=6 -run='^$$' -timeout=120s \
		./internal/bfd/ | tee $(BENCH_DIR)/$(BENCH_VERSION)/benchmark-bfd.txt
	$(EXEC) go test -buildvcs=false -bench=. -benchmem -count=6 -run='^$$' -timeout=120s \
		./internal/netio/ | tee $(BENCH_DIR)/$(BENCH_VERSION)/benchmark-netio.txt
	@cat $(BENCH_DIR)/$(BENCH_VERSION)/benchmark-bfd.txt \
		$(BENCH_DIR)/$(BENCH_VERSION)/benchmark-netio.txt \
		> $(BENCH_DIR)/$(BENCH_VERSION)/benchmark.txt
	@printf '{"version":"%s","commit":"%s","date":"%s","go":"%s","count":6}\n' \
		"$(BENCH_VERSION)" "$(GIT_COMMIT)" "$(BUILD_DATE)" \
		"$$($(EXEC) go env GOVERSION)" \
		> $(BENCH_DIR)/$(BENCH_VERSION)/meta.json
	@echo "=== Benchmarks saved to $(BENCH_DIR)/$(BENCH_VERSION)/ ==="

# Compare benchmarks between two versions via benchstat.
# Usage: make benchmark-compare OLD=v0.4.0 NEW=v0.5.0
OLD ?= baseline
NEW ?= $(BENCH_VERSION)

benchmark-compare:
	@mkdir -p $(REPORT_DIR)/benchmarks
	@old_file=""; new_file=""; \
	if [ "$(OLD)" = "baseline" ]; then old_file="$(BENCH_DIR)/baseline.txt"; \
	else old_file="$(BENCH_DIR)/$(OLD)/benchmark.txt"; fi; \
	new_file="$(BENCH_DIR)/$(NEW)/benchmark.txt"; \
	$(EXEC) benchstat "$$old_file" "$$new_file" | tee $(REPORT_DIR)/benchmarks/comparison.txt; \
	printf '<!DOCTYPE html>\n<html><head><meta charset="utf-8">\n<title>GoBFD Benchmark Comparison: %s vs %s</title>\n<style>\nbody{font-family:system-ui,sans-serif;margin:2em;background:#1a1a2e;color:#e0e0e0}\nh1{color:#00d4ff}pre{background:#16213e;padding:1.5em;border-radius:8px;overflow-x:auto;font-size:14px;line-height:1.6;border:1px solid #0f3460}\n.meta{color:#888;font-size:13px}\n</style></head><body>\n<h1>GoBFD Benchmark Comparison</h1>\n<p class="meta">Old: %s &mdash; New: %s &mdash; Generated: %s</p>\n<pre>\n' \
		"$(OLD)" "$(NEW)" "$(OLD)" "$(NEW)" "$(BUILD_DATE)" \
		> $(REPORT_DIR)/benchmarks/comparison.html; \
	cat $(REPORT_DIR)/benchmarks/comparison.txt >> $(REPORT_DIR)/benchmarks/comparison.html; \
	printf '</pre></body></html>\n' >> $(REPORT_DIR)/benchmarks/comparison.html; \
	echo "=== Comparison: $(REPORT_DIR)/benchmarks/comparison.html ==="

# Generate test report (JUnit XML -> standalone HTML).
# Usage: make test-report
test-report:
	@mkdir -p $(REPORT_DIR)/tests
	$(EXEC) gotestsum \
		--junitfile $(REPORT_DIR)/tests/unit-report.xml \
		--jsonfile $(REPORT_DIR)/tests/unit-report.json \
		--format short-verbose \
		-- -buildvcs=false ./... -race -count=1
	$(EXEC) go run ./test/cmd/junitreport --root . \
		--input $(REPORT_DIR)/tests/unit-report.xml \
		--output $(REPORT_DIR)/tests/unit-report.html
	@echo "=== Test report: $(REPORT_DIR)/tests/unit-report.html ==="

# Full pipeline: tests + benchmarks + comparison.
# Usage: make report-all [OLD=v0.4.0]
report-all: test-report benchmark-save benchmark-compare
	@echo "=== All reports in $(REPORT_DIR)/ ==="

# === Cross-Implementation Benchmarks ===

BENCH_COMPOSE := bench/compose.yml
BENCH_DC := $(COMPOSE) -f $(BENCH_COMPOSE)
BENCH_RESULTS := $(CURDIR)/bench-results
BENCH_REPORT_OUTPUT ?= $(CURDIR)/reports/benchmarks/cross-comparison.html
BENCH_META_JSON ?= $(CURDIR)/testdata/benchmarks/v0.4.0/meta.json
BENCH_REPORT_TEMPLATE := $(CURDIR)/scripts/report-template.html

benchmark-cross:
	@mkdir -p "$(BENCH_RESULTS)"
	BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) build
	BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) run --rm bench-c
	BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) run --rm bench-go
	@echo "=== All benchmarks completed. Results in $(BENCH_RESULTS)/ ==="

benchmark-report:
	GCC_VERSION="gcc (Debian trixie)" go run ./test/cmd/benchreport -- \
		"$(BENCH_RESULTS)/bench-go.txt" \
		"$(BENCH_RESULTS)/bench-c-frr.txt" \
		"$(BENCH_RESULTS)/bench-c-bird.txt" \
		"$(BENCH_META_JSON)" "$(BENCH_REPORT_TEMPLATE)" "$(BENCH_REPORT_OUTPUT)"

# === Quality ===

lint: dev-ensure
	$(EXEC) go run ./test/cmd/cictl lint

# lint-ci deliberately has no Podman dependency so the exact same contract runs
# inside the pinned development container and GitHub's Go job container.
lint-ci:
	go run ./test/cmd/cictl lint

lint-fix:
	$(EXEC) /go/bin/golangci-lint run --fix ./...

gopls-check: dev-ensure
	$(EXEC) env GOMAXPROCS=2 GOMEMLIMIT=1500MiB go run ./test/cmd/repoquality gopls --root .
	$(EXEC) env GOMAXPROCS=2 GOMEMLIMIT=1500MiB go run ./test/cmd/repoquality gopls --root . \
		--goos darwin --profile dependencyinventory_generate

lint-md: dev-ensure
	$(EXEC) go run ./test/cmd/repoquality markdown --root .

lint-yaml: dev-ensure
	$(EXEC) go tool -modfile=tools/go.mod yamlfmt -lint .

lint-spell: dev-ensure
	$(EXEC) go tool -modfile=tools/go.mod misspell -error \
		README.md \
		CONTRIBUTING.md \
		CODE_OF_CONDUCT.md \
		SUPPORT.md \
		GOVERNANCE.md \
		MAINTAINERS.md \
		CHANGELOG.md \
		docs/en/roadmap.md

lint-docs: lint-md lint-yaml lint-spell

lint-commit:
	@test -n "$(MSG)" || (echo "Usage: make lint-commit MSG='feat(bfd): add feature'"; exit 1)
	$(EXEC) go run ./test/cmd/repoquality commit --message "$(MSG)"

semgrep:
	$(SEMGREP) scan $(SEMGREP_COMMON_FLAGS)

semgrep-json:
	$(SEMGREP) scan $(SEMGREP_COMMON_FLAGS) --json

semgrep-pro:
	$(SEMGREP) scan --pro $(SEMGREP_COMMON_FLAGS)

vulncheck:
	$(EXEC) go run ./scripts/vuln-audit.go

osv-scan: vulncheck

vulncheck-strict:
	$(EXEC) govulncheck ./...

osv-scan-strict:
	$(EXEC) osv-scanner scan -r .

# === Proto ===

proto-gen:
	$(EXEC) buf generate

proto-lint: dev-ensure
	$(EXEC) buf lint

proto-breaking:
	$(EXEC) buf breaking --against '.git#branch=master'

proto-update:
	$(EXEC) buf dep update

# === Dependencies ===

dependency-inventory: dev-ensure
	$(EXEC) go run -tags dependencyinventory_generate ./test/cmd/dependencyinventory --root . --output docs/supply-chain/dependency-inventory.json

dependency-inventory-check: dev-ensure
	$(EXEC) go test -race -count=1 ./test/internal/dependencyinventory

tidy:
	$(EXEC) go mod tidy

download:
	$(EXEC) go mod download

# === Clean ===

clean:
	$(EXEC) rm -f gobfd gobfdctl
	$(EXEC) go clean -cache -testcache

# === Info ===

versions:
	@echo "=== Go ===" && $(EXEC) go version
	@echo "=== Buf ===" && $(EXEC) buf --version
	@echo "=== golangci-lint ===" && $(EXEC) /go/bin/golangci-lint version --short
	@echo "=== govulncheck ===" && $(EXEC) govulncheck -version 2>/dev/null || echo "installed"

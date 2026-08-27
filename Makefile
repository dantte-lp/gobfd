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
#   make interop       — run interop tests (bash, builds + tests + cleans up)
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
GOLANGCI_LINT ?= go tool -modfile=tools/go.mod golangci-lint
SEMGREP ?= semgrep
SEMGREP_CONFIG ?= p/golang
SEMGREP_COMMON_FLAGS := --config $(SEMGREP_CONFIG) --metrics=off --disable-version-check --timeout 15 --no-git-ignore --include='*.go' .

.PHONY: all verify build test testcontainers-smoke lint lint-ci lint-fix gopls-check lint-docs lint-md lint-yaml lint-spell lint-commit python-sync python-check semgrep semgrep-json semgrep-pro proto-gen proto-lint fuzz vulncheck osv-scan vulncheck-strict osv-scan-strict dependency-inventory dependency-inventory-check \
        benchmark benchmark-all benchmark-save benchmark-compare \
        test-report report-all \
        coverage profile \
        up down restart logs shell clean tidy \
        dev-ps dev-project dev-ensure \
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

shell:
	$(DC) exec dev bash

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

e2e-core-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_core_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/core/...
	bash -o pipefail -c 'umask 077; report_parent="$(CURDIR)/reports/e2e/core"; \
		mkdir -p "$${report_parent}" || exit 1; chmod 0700 "$${report_parent}" || exit 1; \
		report_dir="$$(mktemp -d "$${report_parent}/run.XXXXXXXX")" || exit 1; \
		chmod 0700 "$${report_dir}" || exit 1; \
		export GOBFD_REQUIRE_PODMAN=1 E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR="$${report_dir}"; \
		go test -tags e2e_core_testcontainers ./test/e2e/core -race -count=1 -json -timeout 10m \
			-run "^TestCoreDaemonTestcontainers$$" | tee "$${report_dir}/go-test.json" "$${report_dir}/go-test.log"; \
		pipeline_status=("$${PIPESTATUS[@]}"); \
		test "$${#pipeline_status[@]}" -eq 2 && test -s "$${report_dir}/go-test.json" && \
			test -s "$${report_dir}/go-test.log" && \
			test "$$(stat -c %a "$${report_dir}/go-test.json")" = 600 && \
			test "$$(stat -c %a "$${report_dir}/go-test.log")" = 600 && \
			test "$${pipeline_status[0]}" -eq 0 && test "$${pipeline_status[1]}" -eq 0'
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

e2e-overlay-test:
	$(EXEC) go test -tags e2e_overlay -v -count=1 ./test/e2e/overlay/

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

# === Interop Tests (FRR 10.7.0 + BIRD 3.3.2 + Holo 0.9.0 + Thoro/bfd — 4-peer topology) ===

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
	$(EXEC) env DOCKER_HOST=unix:///run/podman/podman.sock \
		GOBFD_REQUIRE_PODMAN=1 \
		INTEROP_TESTCONTAINERS_ARTIFACT_DIR=/app/reports/e2e/interop-testcontainers \
		go test -tags interop_testcontainers -race -count=1 -v -timeout 15m \
		-run '^TestFourPeerTopologyTestcontainers$$' ./test/interop/

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
INTEROP_BGP_DC := $(COMPOSE) -f $(INTEROP_BGP_COMPOSE)

interop-bgp: interop-bgp-testcontainers

interop-bgp-test:
	$(EXEC) env INTEROP_BGP_COMPOSE_FILE=$(INTEROP_BGP_COMPOSE) \
		go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/

interop-bgp-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run \
		--build-tags interop_bgp_testcontainers ./test/interop-bgp/...
	$(EXEC) env DOCKER_HOST=unix:///run/podman/podman.sock \
		GOBFD_REQUIRE_PODMAN=1 \
		INTEROP_BGP_TESTCONTAINERS_ARTIFACT_DIR=/app/reports/e2e/interop-bgp-testcontainers \
		go test -tags interop_bgp_testcontainers -race -count=1 -v -timeout 15m \
		-run '^TestBGPBFDTopologyTestcontainers$$' ./test/interop-bgp/

interop-bgp-up:
	$(INTEROP_BGP_DC) up --build -d

interop-bgp-down:
	$(INTEROP_BGP_DC) down --volumes --remove-orphans

interop-bgp-logs:
	$(INTEROP_BGP_DC) logs -f

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

interop-clab-bootstrap:
	go run ./test/cmd/clabbootstrap $(ARGS)

interop-clab:
	$(DC) up -d --build --force-recreate dev
	./test/interop-clab/run.sh

interop-clab-test:
	$(EXEC) go test -tags interop_clab -v -count=1 -timeout 600s ./test/interop-clab/

interop-clab-up:
	$(DC) up -d --build --force-recreate dev
	./test/interop-clab/run.sh --up-only

interop-clab-down:
	./test/interop-clab/run.sh --down-only

# === Integration Examples ===

INT_BGP_DC := $(COMPOSE) -f deployments/integrations/bgp-fast-failover/compose.yml
INT_HAPROXY_DC := $(COMPOSE) -f deployments/integrations/haproxy-health/compose.yml
INT_OBS_DC := $(COMPOSE) -f deployments/integrations/observability/compose.yml
INT_EXABGP_DC := $(COMPOSE) -f deployments/integrations/exabgp-anycast/compose.yml

int-bgp-failover: int-bgp-failover-testcontainers

int-bgp-failover-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_bgp_failover_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/bgp-failover/...
	bash -o pipefail -c 'umask 077; report_parent="$(CURDIR)/reports/e2e/bgp-fast-failover"; \
		mkdir -p "$${report_parent}" || exit 1; chmod 0700 "$${report_parent}" || exit 1; \
		report_dir="$$(mktemp -d "$${report_parent}/run.XXXXXXXX")" || exit 1; \
		chmod 0700 "$${report_dir}" || exit 1; \
		export GOBFD_REQUIRE_PODMAN=1 E2E_BGP_FAILOVER_TESTCONTAINERS_ARTIFACT_DIR="$${report_dir}"; \
		go test -tags e2e_bgp_failover_testcontainers ./test/e2e/bgp-failover -race -count=1 -json -timeout 10m \
			-run "^TestBGPFastFailoverTestcontainers$$" | tee "$${report_dir}/go-test.json" "$${report_dir}/go-test.log"; \
		pipeline_status=("$${PIPESTATUS[@]}"); \
		test "$${#pipeline_status[@]}" -eq 2 && test -s "$${report_dir}/go-test.json" && \
			test -s "$${report_dir}/go-test.log" && \
			test "$$(stat -c %a "$${report_dir}/go-test.json")" = 600 && \
			test "$$(stat -c %a "$${report_dir}/go-test.log")" = 600 && \
			test "$${pipeline_status[0]}" -eq 0 && test "$${pipeline_status[1]}" -eq 0'

int-bgp-failover-up:
	$(INT_BGP_DC) up --build -d

int-bgp-failover-down:
	$(INT_BGP_DC) down --volumes --remove-orphans

int-bgp-failover-logs:
	$(INT_BGP_DC) logs -f

int-haproxy:
	./deployments/integrations/haproxy-health/run.sh

int-haproxy-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_haproxy_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/haproxy-health/...
	bash -o pipefail -c 'umask 077; run_id="$$(date -u +%Y%m%dT%H%M%SZ)"; \
		report_dir="$(CURDIR)/reports/e2e/haproxy-health/$${run_id}"; mkdir -p "$${report_dir}"; \
		export GOBFD_REQUIRE_PODMAN=1 E2E_HAPROXY_TESTCONTAINERS_ARTIFACT_DIR="$${report_dir}"; \
		go test -trimpath -tags e2e_haproxy_testcontainers ./test/e2e/haproxy-health -race -count=1 -json -timeout 10m \
			| tee "$${report_dir}/go-test.json" "$${report_dir}/go-test.log"; \
		test_status="$${PIPESTATUS[0]}"; test -s "$${report_dir}/go-test.json" && \
			test -s "$${report_dir}/go-test.log" && test "$${test_status}" -eq 0'

int-haproxy-up:
	$(INT_HAPROXY_DC) up --build -d

int-haproxy-down:
	$(INT_HAPROXY_DC) down --volumes --remove-orphans

int-haproxy-logs:
	$(INT_HAPROXY_DC) logs -f

int-observability:
	./deployments/integrations/observability/run.sh

int-observability-testcontainers: dev-ensure
	$(EXEC) /go/bin/golangci-lint run --build-tags e2e_observability_testcontainers \
		./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/observability/...
	bash -o pipefail -c 'umask 077; working_dir="$$(pwd -P)" || exit 1; \
		report_parent="$${working_dir}/reports/e2e/observability"; \
		mkdir -p "$${report_parent}" || exit 1; chmod 0700 "$${report_parent}" || exit 1; \
		report_dir="$$(mktemp -d "$${report_parent}/run.XXXXXXXX")" || exit 1; \
		chmod 0700 "$${report_dir}" || exit 1; report_owner="$$(basename "$${report_dir}")" || exit 1; \
		printf "%s\n" "$${report_owner}" > "$${report_dir}/.gobfd-observability-owner"; \
		printf_status="$$?"; test "$${printf_status}" -eq 0 || exit 1; \
		chmod 0600 "$${report_dir}/.gobfd-observability-owner" || exit 1; \
		export GOBFD_REQUIRE_PODMAN=1 E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR="$${report_dir}" \
			E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER="$${report_owner}"; \
		go test -trimpath -tags e2e_observability_testcontainers ./test/e2e/observability -race -count=1 -json -timeout 15m \
			| tee "$${report_dir}/go-test.json" "$${report_dir}/go-test.log"; \
		pipeline_status=("$${PIPESTATUS[@]}"); \
		test "$${#pipeline_status[@]}" -eq 2 && test -s "$${report_dir}/go-test.json" && \
			test -s "$${report_dir}/go-test.log" && test "$${pipeline_status[0]}" -eq 0 && \
			test "$${pipeline_status[1]}" -eq 0'

int-observability-up:
	$(INT_OBS_DC) up --build -d

int-observability-down:
	$(INT_OBS_DC) down --volumes --remove-orphans

int-observability-logs:
	$(INT_OBS_DC) logs -f

int-exabgp-anycast:
	./deployments/integrations/exabgp-anycast/run.sh

int-exabgp-anycast-up:
	$(INT_EXABGP_DC) up --build -d

int-exabgp-anycast-down:
	$(INT_EXABGP_DC) down --volumes --remove-orphans

int-exabgp-anycast-logs:
	$(INT_EXABGP_DC) logs -f

int-k8s:
	./deployments/integrations/kubernetes/run.sh

int-k8s-up:
	./deployments/integrations/kubernetes/setup-cluster.sh && kubectl apply -f deployments/integrations/kubernetes/manifests/

int-k8s-down:
	./deployments/integrations/kubernetes/teardown.sh

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
	$(EXEC) uv run --frozen --no-default-groups --group quality -- \
		junit2html $(REPORT_DIR)/tests/unit-report.xml \
		$(REPORT_DIR)/tests/unit-report.html
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

LINT_ENABLED_COUNT := 92

define run_lint_tag
	@tag='$(1)'; \
		files="$$(grep -RIl --include='*.go' "^//go:build .*\\<$$tag\\>" .)"; \
		constrained="$$(printf '%s\n' "$$files" | sed '/^$$/d' | wc -l)"; \
		inputs="$$(go list -buildvcs=false -tags "$$tag" -f '{{range .GoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}{{range .CgoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}' $(2))"; \
		input_count="$$(printf '%s\n' "$$inputs" | sed '/^$$/d' | wc -l)"; \
		test "$$constrained" -gt 0 || { echo "lint-ci: tag $$tag has no constrained source files"; exit 1; }; \
		test "$$input_count" -gt 0 || { echo "lint-ci: tag $$tag produced no Go inputs"; exit 1; }; \
		echo "lint-ci: tag $$tag ($$constrained constrained files, $$input_count Go inputs)"
	$(GOLANGCI_LINT) run --build-tags '$(1)' ./...
endef

lint: dev-ensure
	$(EXEC) make lint-ci

# lint-ci deliberately has no Podman dependency so the exact same contract runs
# inside the pinned development container and GitHub's Go job container.
lint-ci:
	@test -e /.dockerenv || test -e /run/.containerenv || { \
		echo "lint-ci is container-only; use 'make lint' locally"; exit 2; \
	}
	$(GOLANGCI_LINT) config verify
	@packages="$$(go list -buildvcs=false -f '{{.ImportPath}}' ./...)"; \
		package_count="$$(printf '%s\n' "$$packages" | sed '/^$$/d' | wc -l)"; \
		production_inputs="$$(go list -buildvcs=false -f '{{range .GoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}{{range .CgoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}' ./...)"; \
		production_input_count="$$(printf '%s\n' "$$production_inputs" | sed '/^$$/d' | wc -l)"; \
		test_inputs="$$(go list -buildvcs=false -f '{{range .TestGoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$$.ImportPath}}/{{.}}{{"\n"}}{{end}}' ./...)"; \
		test_input_count="$$(printf '%s\n' "$$test_inputs" | sed '/^$$/d' | wc -l)"; \
		test "$$package_count" -gt 0 || { echo "lint-ci: base selector produced no packages"; exit 1; }; \
		test "$$production_input_count" -gt 0 || { echo "lint-ci: base selector produced no production Go inputs"; exit 1; }; \
		test "$$test_input_count" -gt 0 || { echo "lint-ci: base selector produced no test Go inputs"; exit 1; }; \
		echo "lint-ci: base ($$package_count packages, $$production_input_count production inputs, $$test_input_count test inputs)"
	@module_version="$$(go list -modfile=tools/go.mod -m -f '{{.Version}}' github.com/golangci/golangci-lint/v2)"; \
		binary_version="v$$($(GOLANGCI_LINT) version --short)"; \
		test "$$module_version" = "$$binary_version" || { \
			echo "lint-ci: module $$module_version != binary $$binary_version"; exit 1; \
		}; \
		linters="$$($(GOLANGCI_LINT) linters)"; \
		enabled="$$(printf '%s\n' "$$linters" | awk '/^Enabled by your configuration/{active=1;next}/^Disabled by your configuration/{active=0} active && /^[a-z0-9_]+:/{count++} END{print count+0}')"; \
		deprecated="$$(printf '%s\n' "$$linters" | awk '/^Enabled by your configuration/{active=1;next}/^Disabled by your configuration/{active=0} active && /\[deprecated\]/{count++} END{print count+0}')"; \
		test "$$enabled" -eq "$(LINT_ENABLED_COUNT)" || { \
			echo "lint-ci: enabled $$enabled linters, expected $(LINT_ENABLED_COUNT)"; exit 1; \
		}; \
		test "$$deprecated" -eq 0 || { \
			echo "lint-ci: $$deprecated deprecated linters are enabled"; exit 1; \
		}; \
		echo "lint-ci: $$enabled linters enabled, 0 deprecated, version $$binary_version"
	$(GOLANGCI_LINT) run ./...
	$(call run_lint_tag,integration,./test/integration/...)
	$(call run_lint_tag,testcontainers,./test/internal/containertest/...)
	$(call run_lint_tag,e2e_core_testcontainers,./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/core/...)
	$(call run_lint_tag,e2e_bgp_failover_testcontainers,./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/bgp-failover/...)
	$(call run_lint_tag,e2e_haproxy_testcontainers,./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/haproxy-health/...)
	$(call run_lint_tag,e2e_observability_testcontainers,./test/internal/podmanapi/... ./test/internal/containertest/... ./test/e2e/observability/...)
	$(call run_lint_tag,e2e_linux,./test/e2e/linux/...)
	$(call run_lint_tag,e2e_overlay,./test/e2e/overlay/...)
	$(call run_lint_tag,e2e_vendor,./test/e2e/vendor/...)
	$(call run_lint_tag,interop,./test/interop/...)
	$(call run_lint_tag,interop_testcontainers,./test/interop/...)
	$(call run_lint_tag,interop_bgp,./test/interop-bgp/...)
	$(call run_lint_tag,interop_bgp_testcontainers,./test/interop-bgp/...)
	$(call run_lint_tag,interop_clab,./test/interop-clab/...)
	$(call run_lint_tag,interop_rfc,./test/interop-rfc/...)
	$(call run_lint_tag,interop_rfc_testcontainers,./test/interop-rfc/...)
	$(call run_lint_tag,dependencyinventory_generate,./test/cmd/dependencyinventory/...)

lint-fix:
	$(EXEC) /go/bin/golangci-lint run --fix ./...

gopls-check: dev-ensure
	$(EXEC) sh ./scripts/gopls-check.sh
	$(EXEC) env GOPLS_GOOS=darwin GOPLS_TAGS=dependencyinventory_generate sh ./scripts/gopls-check.sh

lint-md: dev-ensure
	$(EXEC) go run ./test/cmd/repoquality markdown --root .

lint-yaml: dev-ensure
	$(EXEC) uv run --frozen --no-default-groups --group quality -- \
		yamllint -c .yamllint.yaml .

lint-spell: dev-ensure
	$(EXEC) uv run --frozen --no-default-groups --group quality -- codespell \
		README.md \
		CONTRIBUTING.md \
		CODE_OF_CONDUCT.md \
		SUPPORT.md \
		GOVERNANCE.md \
		MAINTAINERS.md \
		CHANGELOG.md \
		docs/en/roadmap.md

lint-docs: lint-md lint-yaml lint-spell

PYTHON_FILES := test/interop-clab/vendor_images.py

python-sync:
	uv lock --check
	uv sync --frozen --all-groups
	@test "$$(uv run --frozen --all-groups python -c 'import sys; print(sys.version.split()[0])')" = "3.14.7"

python-check: python-sync
	@test "$$(printf '%s\n' $(PYTHON_FILES) | sed '/^$$/d' | wc -l)" -eq 1
	uv run --frozen --no-default-groups --group quality -- ruff check $(PYTHON_FILES)
	uv run --frozen --no-default-groups --group quality -- ty check $(PYTHON_FILES)
	uv run --frozen --no-default-groups --group quality -- \
		bandit -q -c pyproject.toml -r $(PYTHON_FILES)
	uv run --frozen --all-groups -- pip-audit --local --progress-spinner off

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

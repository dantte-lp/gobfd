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
#   make interop-up    — start interop test stack (4 peers)
#   make interop-down  — stop interop test stack
#   make integration   — alias for interop

PROJECT_SLUG := $(shell basename "$(CURDIR)" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9_-]+/-/g; s/^-+//; s/-+$$//')
COMPOSE_PROJECT_NAME ?= $(PROJECT_SLUG)
COMPOSE_FILE := deployments/compose/compose.dev.yml
DC := COMPOSE_PROJECT_NAME=$(COMPOSE_PROJECT_NAME) podman-compose -p $(COMPOSE_PROJECT_NAME) -f $(COMPOSE_FILE)
EXEC := $(DC) exec -T dev
SEMGREP ?= semgrep
SEMGREP_CONFIG ?= p/golang
SEMGREP_COMMON_FLAGS := --config $(SEMGREP_CONFIG) --metrics=off --disable-version-check --timeout 15 --no-git-ignore --include='*.go' .

.PHONY: all verify build test lint lint-fix gopls-check lint-docs lint-md lint-yaml lint-spell lint-commit semgrep semgrep-json semgrep-pro proto-gen proto-lint fuzz vulncheck osv-scan vulncheck-strict osv-scan-strict \
        benchmark benchmark-all benchmark-save benchmark-compare \
        test-report report-all \
        coverage profile \
        up down restart logs shell clean tidy \
        dev-ps dev-project dev-ensure \
        e2e-help e2e-core e2e-core-test e2e-core-up e2e-core-down e2e-core-logs \
        e2e-routing e2e-routing-test e2e-rfc e2e-rfc-test e2e-overlay e2e-overlay-test e2e-linux e2e-linux-test e2e-vendor e2e-vendor-test \
        interop interop-test interop-up interop-down interop-logs \
        interop-capture interop-pcap interop-pcap-summary integration \
        interop-bgp interop-bgp-test interop-bgp-up interop-bgp-down interop-bgp-logs \
        interop-rfc interop-rfc-test interop-rfc-up interop-rfc-down interop-rfc-logs \
        interop-clab interop-clab-test interop-clab-up interop-clab-down \
        int-bgp-failover int-bgp-failover-up int-bgp-failover-down int-bgp-failover-logs \
        int-haproxy int-haproxy-up int-haproxy-down int-haproxy-logs \
        int-observability int-observability-up int-observability-down int-observability-logs \
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
	cname="$(COMPOSE_PROJECT_NAME)_dev_1"; \
	if ! podman inspect "$$cname" >/dev/null 2>&1; then \
	  echo "dev-ensure: container $$cname missing, creating"; \
	  $(DC) up -d --build dev; \
	  exit 0; \
	fi; \
	src="$$(podman inspect "$$cname" --format '{{range .Mounts}}{{if eq .Destination "/app"}}{{.Source}}{{end}}{{end}}')"; \
	if [ "$$src" != "$(CURDIR)" ]; then \
	  echo "dev-ensure: mount source $$src != $(CURDIR), force recreating"; \
	  $(DC) up -d --build --force-recreate dev; \
	  exit 0; \
	fi; \
	if ! podman inspect "$$cname" --format '{{.State.Running}}' | grep -q true; then \
	  echo "dev-ensure: container $$cname not running, starting"; \
	  podman start "$$cname" >/dev/null; \
	fi

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

E2E_CORE_COMPOSE := test/e2e/core/compose.yml
E2E_CORE_PROJECT ?= $(COMPOSE_PROJECT_NAME)-e2e-core
E2E_CORE_DC := podman-compose -p $(E2E_CORE_PROJECT) -f $(E2E_CORE_COMPOSE)

e2e-core:
	$(DC) up -d --build --force-recreate dev
	./test/e2e/core/run.sh

e2e-core-test:
	$(EXEC) env E2E_CORE_COMPOSE_FILE=$(E2E_CORE_COMPOSE) \
		E2E_CORE_PROJECT=$(E2E_CORE_PROJECT) \
		go test -tags e2e_core -v -count=1 -timeout 300s ./test/e2e/core/

e2e-core-up:
	$(E2E_CORE_DC) up --build -d

e2e-core-down:
	$(E2E_CORE_DC) down --volumes --remove-orphans

e2e-core-logs:
	$(E2E_CORE_DC) logs -f

e2e-routing:
	$(DC) up -d --build --force-recreate dev
	./test/e2e/routing/run.sh

e2e-routing-test:
	$(EXEC) env INTEROP_COMPOSE_FILE=/app/test/interop/compose.yml \
		go test -tags interop -v -count=1 -timeout 300s ./test/interop/
	$(EXEC) env INTEROP_BGP_COMPOSE_FILE=/app/test/interop-bgp/compose.yml \
		go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/

e2e-rfc:
	$(DC) up -d --build --force-recreate dev
	./test/e2e/rfc/run.sh

e2e-rfc-test:
	$(EXEC) env INTEROP_RFC_COMPOSE_FILE=/app/test/interop-rfc/compose.yml \
		go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/

e2e-overlay:
	$(DC) up -d --build --force-recreate dev
	./test/e2e/overlay/run.sh

e2e-overlay-test:
	$(EXEC) go test -tags e2e_overlay -v -count=1 ./test/e2e/overlay/

e2e-linux:
	$(DC) up -d --build --force-recreate dev
	./test/e2e/linux/run.sh

e2e-linux-test: e2e-linux

e2e-vendor:
	$(DC) up -d --build --force-recreate dev
	./test/e2e/vendor/run.sh

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

test-run:
	@test -n "$(RUN)" || (echo "Usage: make test-run RUN=TestFSMTransition PKG=./internal/bfd"; exit 1)
	$(EXEC) go test -run '$(RUN)' $(PKG) -race -count=1 -v

fuzz:
	@test -n "$(FUNC)" || (echo "Usage: make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd"; exit 1)
	$(EXEC) go test -fuzz=$(FUNC) $(PKG) -fuzztime=60s

test-integration:
	$(EXEC) go test -tags integration ./test/integration/ -race -count=1 -v

# === Interop Tests (FRR + BIRD3 + aiobfd + Thoro/bfd — 4-peer topology) ===

INTEROP_COMPOSE := test/interop/compose.yml
INTEROP_DC := podman-compose -f $(INTEROP_COMPOSE)

interop:
	./test/interop/run.sh

interop-test:
	$(EXEC) env INTEROP_COMPOSE_FILE=$(INTEROP_COMPOSE) \
		go test -tags interop -v -count=1 -timeout 300s ./test/interop/

interop-up:
	$(INTEROP_DC) up --build -d

interop-down:
	$(INTEROP_DC) down --volumes --remove-orphans

interop-logs:
	$(INTEROP_DC) logs -f

interop-capture:
	podman exec tshark-interop tshark -i any -f "udp port 3784" -V

interop-pcap:
	podman exec tshark-interop tshark -r /captures/bfd.pcapng -V -Y bfd

interop-pcap-summary:
	podman exec tshark-interop tshark -r /captures/bfd.pcapng -Y bfd \
		-T fields -e frame.time_relative -e ip.src -e ip.dst \
		-e bfd.version -e bfd.diag -e bfd.sta -e bfd.flags \
		-e bfd.detect_time_multiplier -e bfd.my_discriminator \
		-e bfd.your_discriminator -e bfd.desired_min_tx_interval \
		-e bfd.required_min_rx_interval -E header=y -E separator=,

integration: interop

# === BGP+BFD Interop Tests (GoBGP + FRR + BIRD3 + ExaBGP — 3 scenarios) ===

INTEROP_BGP_COMPOSE := test/interop-bgp/compose.yml
INTEROP_BGP_DC := podman-compose -f $(INTEROP_BGP_COMPOSE)

interop-bgp:
	./test/interop-bgp/run.sh

interop-bgp-test:
	$(EXEC) env INTEROP_BGP_COMPOSE_FILE=$(INTEROP_BGP_COMPOSE) \
		go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/

interop-bgp-up:
	$(INTEROP_BGP_DC) up --build -d

interop-bgp-down:
	$(INTEROP_BGP_DC) down --volumes --remove-orphans

interop-bgp-logs:
	$(INTEROP_BGP_DC) logs -f

# === RFC Interop Tests (RFC 7419 + RFC 9384 + RFC 9468 + RFC 9747) ===

INTEROP_RFC_COMPOSE := test/interop-rfc/compose.yml
INTEROP_RFC_DC := podman-compose -f $(INTEROP_RFC_COMPOSE)

interop-rfc:
	./test/interop-rfc/run.sh

interop-rfc-test:
	$(EXEC) env INTEROP_RFC_COMPOSE_FILE=$(INTEROP_RFC_COMPOSE) \
		go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/

interop-rfc-up:
	$(INTEROP_RFC_DC) up --build -d

interop-rfc-down:
	$(INTEROP_RFC_DC) down --volumes --remove-orphans

interop-rfc-logs:
	$(INTEROP_RFC_DC) logs -f

# === Containerlab Vendor Interop Tests (Arista + Nokia + Cisco + SONiC + VyOS) ===

CLAB_TOPO := test/interop-clab/gobfd-vendors.clab.yml

interop-clab:
	$(DC) up -d --build --force-recreate dev
	./test/interop-clab/run.sh

interop-clab-test:
	$(EXEC) go test -tags interop_clab -v -count=1 -timeout 600s ./test/interop-clab/

interop-clab-up:
	$(DC) up -d --build --force-recreate dev
	./test/interop-clab/run.sh --up-only

interop-clab-down:
	containerlab --runtime podman destroy -t $(CLAB_TOPO) --cleanup 2>/dev/null || true
	podman rm -f clab-gobfd-vendors-gobfd clab-gobfd-vendors-arista clab-gobfd-vendors-nokia clab-gobfd-vendors-cisco clab-gobfd-vendors-sonic clab-gobfd-vendors-vyos clab-gobfd-vendors-frr 2>/dev/null || true
	ip link del veth-eth1 2>/dev/null || true
	ip link del veth-eth2 2>/dev/null || true
	ip link del veth-eth3 2>/dev/null || true
	ip link del veth-eth4 2>/dev/null || true
	ip link del veth-eth5 2>/dev/null || true
	ip link del veth-eth6 2>/dev/null || true
	podman network rm gobfd-vendors-net 2>/dev/null || true

# === Integration Examples ===

INT_BGP_DC := podman-compose -f deployments/integrations/bgp-fast-failover/compose.yml
INT_HAPROXY_DC := podman-compose -f deployments/integrations/haproxy-health/compose.yml
INT_OBS_DC := podman-compose -f deployments/integrations/observability/compose.yml
INT_EXABGP_DC := podman-compose -f deployments/integrations/exabgp-anycast/compose.yml

int-bgp-failover:
	./deployments/integrations/bgp-fast-failover/run.sh

int-bgp-failover-up:
	$(INT_BGP_DC) up --build -d

int-bgp-failover-down:
	$(INT_BGP_DC) down --volumes --remove-orphans

int-bgp-failover-logs:
	$(INT_BGP_DC) logs -f

int-haproxy:
	./deployments/integrations/haproxy-health/run.sh

int-haproxy-up:
	$(INT_HAPROXY_DC) up --build -d

int-haproxy-down:
	$(INT_HAPROXY_DC) down --volumes --remove-orphans

int-haproxy-logs:
	$(INT_HAPROXY_DC) logs -f

int-observability:
	./deployments/integrations/observability/run.sh

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
	$(EXEC) junit2html $(REPORT_DIR)/tests/unit-report.xml \
		$(REPORT_DIR)/tests/unit-report.html
	@echo "=== Test report: $(REPORT_DIR)/tests/unit-report.html ==="

# Full pipeline: tests + benchmarks + comparison.
# Usage: make report-all [OLD=v0.4.0]
report-all: test-report benchmark-save benchmark-compare
	@echo "=== All reports in $(REPORT_DIR)/ ==="

# === Cross-Implementation Benchmarks ===

BENCH_COMPOSE := bench/compose.yml
BENCH_DC := podman-compose -f $(BENCH_COMPOSE)
BENCH_RESULTS := bench-results

benchmark-cross:
	@mkdir -p $(BENCH_RESULTS)
	$(BENCH_DC) build
	$(BENCH_DC) run --rm bench-c
	$(BENCH_DC) run --rm bench-python
	$(BENCH_DC) run --rm bench-go
	@echo "=== All benchmarks completed. Results in $(BENCH_RESULTS)/ ==="

benchmark-report:
	./scripts/gen-report.sh $(BENCH_RESULTS)

# === Quality ===

lint:
	$(EXEC) golangci-lint run ./...

lint-fix:
	$(EXEC) golangci-lint run --fix ./...

gopls-check:
	$(EXEC) sh ./scripts/gopls-check.sh

lint-md:
	$(EXEC) markdownlint-cli2 "**/*.md" "#node_modules" "#vendor" "#reports" "#dist" "#build" "#docs/rfc"

lint-yaml:
	$(EXEC) yamllint -c .yamllint.yaml .

lint-spell: dev-ensure
	$(EXEC) cspell --no-progress --no-summary --config .cspell.json \
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
	$(EXEC) sh -c 'printf "%s\n" "$$1" | commitlint --config .commitlintrc.yaml' sh "$(MSG)"

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

proto-lint:
	$(EXEC) buf lint

proto-breaking:
	$(EXEC) buf breaking --against '.git#branch=master'

proto-update:
	$(EXEC) buf dep update

# === Dependencies ===

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
	@echo "=== golangci-lint ===" && $(EXEC) golangci-lint version --short
	@echo "=== govulncheck ===" && $(EXEC) govulncheck -version 2>/dev/null || echo "installed"

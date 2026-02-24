# GoBFD Makefile — all Go commands run inside Podman containers.
# NO local Go toolchain required.
#
# Usage:
#   make up          — start dev container
#   make build       — compile both binaries
#   make test        — run all tests with race detector
#   make lint        — run golangci-lint v2
#   make proto-gen   — generate protobuf Go code
#   make proto-lint  — lint proto definitions
#   make all           — build + test + lint
#   make interop       — run interop tests (bash, builds + tests + cleans up)
#   make interop-test  — run Go interop tests (stack must be running)
#   make interop-up    — start interop test stack (4 peers)
#   make interop-down  — stop interop test stack
#   make integration   — alias for interop

COMPOSE_FILE := deployments/compose/compose.dev.yml
DC := podman-compose -f $(COMPOSE_FILE)
EXEC := $(DC) exec -T dev

.PHONY: all build test lint proto-gen proto-lint fuzz vulncheck osv-scan \
        up down restart logs shell clean tidy \
        interop interop-test interop-up interop-down interop-logs \
        interop-capture interop-pcap interop-pcap-summary integration \
        interop-bgp interop-bgp-test interop-bgp-up interop-bgp-down interop-bgp-logs \
        interop-rfc interop-rfc-test interop-rfc-up interop-rfc-down interop-rfc-logs \
        interop-clab interop-clab-test interop-clab-up interop-clab-down \
        int-bgp-failover int-bgp-failover-up int-bgp-failover-down int-bgp-failover-logs \
        int-haproxy int-haproxy-up int-haproxy-down int-haproxy-logs \
        int-observability int-observability-up int-observability-down int-observability-logs \
        int-exabgp-anycast int-exabgp-anycast-up int-exabgp-anycast-down int-exabgp-anycast-logs \
        int-k8s int-k8s-up int-k8s-down

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

# === Build ===

all: build test lint

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/dantte-lp/gobfd/internal/version.Version=$(VERSION) \
  -X github.com/dantte-lp/gobfd/internal/version.GitCommit=$(GIT_COMMIT) \
  -X github.com/dantte-lp/gobfd/internal/version.BuildDate=$(BUILD_DATE)

build:
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfd
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfdctl
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfd-haproxy-agent
	$(EXEC) go build -ldflags='$(LDFLAGS)' ./cmd/gobfd-exabgp-bridge

# === Test ===

test:
	$(EXEC) go test ./... -race -count=1

test-v:
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
	INTEROP_COMPOSE_FILE=$(INTEROP_COMPOSE) go test -tags interop -v -count=1 -timeout 300s ./test/interop/

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
	INTEROP_BGP_COMPOSE_FILE=$(INTEROP_BGP_COMPOSE) go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/

interop-bgp-up:
	$(INTEROP_BGP_DC) up --build -d

interop-bgp-down:
	$(INTEROP_BGP_DC) down --volumes --remove-orphans

interop-bgp-logs:
	$(INTEROP_BGP_DC) logs -f

# === RFC Interop Tests (RFC 7419 + RFC 9384 + RFC 9468 — 3 scenarios) ===

INTEROP_RFC_COMPOSE := test/interop-rfc/compose.yml
INTEROP_RFC_DC := podman-compose -f $(INTEROP_RFC_COMPOSE)

interop-rfc:
	./test/interop-rfc/run.sh

interop-rfc-test:
	INTEROP_RFC_COMPOSE_FILE=$(INTEROP_RFC_COMPOSE) go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/

interop-rfc-up:
	$(INTEROP_RFC_DC) up --build -d

interop-rfc-down:
	$(INTEROP_RFC_DC) down --volumes --remove-orphans

interop-rfc-logs:
	$(INTEROP_RFC_DC) logs -f

# === Containerlab Vendor Interop Tests (Arista + Nokia + Cisco + SONiC + VyOS) ===

CLAB_TOPO := test/interop-clab/gobfd-vendors.clab.yml

interop-clab:
	./test/interop-clab/run.sh

interop-clab-test:
	go test -tags interop_clab -v -count=1 -timeout 600s ./test/interop-clab/

interop-clab-up:
	./test/interop-clab/run.sh --up-only

interop-clab-down:
	containerlab --runtime podman destroy -t $(CLAB_TOPO) --cleanup 2>/dev/null || true
	podman rm -f clab-gobfd-vendors-gobfd 2>/dev/null || true

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

# === Quality ===

lint:
	$(EXEC) golangci-lint run ./...

lint-fix:
	$(EXEC) golangci-lint run --fix ./...

vulncheck:
	$(EXEC) govulncheck ./...

osv-scan:
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

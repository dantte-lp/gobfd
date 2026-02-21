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
#   make all         — build + test + lint
#   make interop     — run interop tests (FRR + BIRD3)
#   make interop-up  — start interop test stack
#   make interop-down — stop interop test stack

COMPOSE_FILE := deployments/compose/compose.dev.yml
DC := podman-compose -f $(COMPOSE_FILE)
EXEC := $(DC) exec -T dev

.PHONY: all build test lint proto-gen proto-lint fuzz vulncheck \
        up down restart logs shell clean tidy \
        interop interop-up interop-down interop-logs \
        interop-capture interop-pcap interop-pcap-summary

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

build:
	$(EXEC) go build ./cmd/gobfd
	$(EXEC) go build ./cmd/gobfdctl

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

# === Interop Tests (FRR + BIRD3) ===

INTEROP_COMPOSE := test/interop/compose.yml
INTEROP_DC := podman-compose -f $(INTEROP_COMPOSE)

interop:
	./test/interop/run.sh

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

# === Quality ===

lint:
	$(EXEC) golangci-lint run ./...

lint-fix:
	$(EXEC) golangci-lint run --fix ./...

vulncheck:
	$(EXEC) govulncheck ./...

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

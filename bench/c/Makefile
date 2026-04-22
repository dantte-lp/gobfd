# bench/c/Makefile — Build BFD cross-implementation C benchmarks.
#
# Usage:
#   make          — build both FRR and BIRD benchmarks
#   make run      — build and run both
#   make clean    — remove binaries

CC      ?= gcc
CFLAGS  := -O2 -Wall -Wextra -std=c11 -D_DEFAULT_SOURCE -static
LDFLAGS :=

BINS := bench_frr bench_bird

.PHONY: all run clean

all: $(BINS)

bench_frr: bench_frr.c bfd_common.h bench_harness.h
	$(CC) $(CFLAGS) -o $@ $< $(LDFLAGS)

bench_bird: bench_bird.c bfd_common.h bench_harness.h
	$(CC) $(CFLAGS) -o $@ $< $(LDFLAGS)

run: all
	@echo "=== FRR-style benchmarks ==="
	./bench_frr
	@echo ""
	@echo "=== BIRD-style benchmarks ==="
	./bench_bird

clean:
	rm -f $(BINS)

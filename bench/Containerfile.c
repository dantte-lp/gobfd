# bench/Containerfile.c — Build and run BFD C benchmarks (FRR + BIRD style).
#
# Single-stage build: Debian trixie + gcc, compile and run in same container.
# Results are written to /results/ (mount a volume).

FROM docker.io/library/debian:trixie-slim@sha256:8aa5a7bf5a1955bddbe7d74fcdd0b91ab30e74362a8b73af672b0481b35b44a0

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /bench
COPY c/ ./

RUN make all

WORKDIR /results

CMD ["sh", "-c", "/bench/bench_frr > /results/bench-c-frr.txt 2>/dev/null && /bench/bench_bird > /results/bench-c-bird.txt 2>/dev/null && echo 'C benchmarks complete'"]

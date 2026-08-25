# bench/Containerfile.c — Build and run BFD C benchmarks (FRR + BIRD style).
#
# Single-stage build: Debian trixie + gcc, compile and run in same container.
# Results are written to /results/ (mount a volume).

FROM docker.io/library/debian:trixie-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /bench
COPY c/ ./

RUN make all

WORKDIR /results

CMD ["sh", "-c", "/bench/bench_frr > /results/bench-c-frr.txt 2>/dev/null && /bench/bench_bird > /results/bench-c-bird.txt 2>/dev/null && echo 'C benchmarks complete'"]

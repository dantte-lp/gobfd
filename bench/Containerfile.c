# bench/Containerfile.c — Build and run BFD C benchmarks (FRR + BIRD style).
#
# Single-stage build: Debian trixie + gcc, compile and run in same container.
# Results are written to /results/ (mount a volume).

FROM docker.io/library/debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /bench
COPY c/ ./

RUN make all

WORKDIR /results

CMD ["sh", "-c", "/bench/bench_frr > /results/bench-c-frr.txt 2>/dev/null && /bench/bench_bird > /results/bench-c-bird.txt 2>/dev/null && echo 'C benchmarks complete'"]

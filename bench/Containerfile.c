# bench/Containerfile.c — Build and run BFD C benchmarks (FRR + BIRD style).
#
# Single-stage build: Alpine + gcc, compile and run in same container.
# Results are written to /results/ (mount a volume).

FROM docker.io/alpine:3.21

RUN apk add --no-cache gcc musl-dev make

WORKDIR /bench
COPY c/ ./

RUN make all

WORKDIR /results

CMD ["sh", "-c", "/bench/bench_frr > /results/bench-c-frr.txt 2>/dev/null && /bench/bench_bird > /results/bench-c-bird.txt 2>/dev/null && echo 'C benchmarks complete'"]

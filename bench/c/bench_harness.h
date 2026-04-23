/*
 * bench_harness.h — Portable micro-benchmark harness for BFD cross-implementation tests.
 *
 * Uses clock_gettime(CLOCK_MONOTONIC) for nanosecond-precision timing.
 * Output format: BENCH<tab>impl<tab>name<tab>ns_per_op<tab>iterations
 * This format is parsed by scripts/gen-report.sh.
 *
 * Anti-optimization: accumulates results into a volatile global sink
 * to prevent the compiler from eliminating benchmark code.
 */
#ifndef BENCH_HARNESS_H
#define BENCH_HARNESS_H

#include <time.h>
#include <stdio.h>
#include <stdint.h>

/* Benchmark parameters — match Go's -count=6 convention */
#define BENCH_ITERATIONS   10000000   /* 10M iterations per run */
#define BENCH_WARMUP       1000000    /* 1M warmup iterations */
#define BENCH_REPEATS      6          /* 6 statistical samples */

/* Global volatile sink — prevents dead code elimination.
 * Each benchmark XORs its result into this value. */
volatile uint64_t bench_sink = 0;

/* Get current time in nanoseconds */
static inline uint64_t now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}

/*
 * BENCH_RUN — run a benchmark and print results.
 *
 * Parameters:
 *   impl  — implementation name string (e.g., "frr", "bird")
 *   name  — benchmark name string (e.g., "Marshal")
 *   setup — code to run once before timing (can be empty: {})
 *   code  — code to run in the hot loop
 *
 * The 'code' block receives the loop variable '_i' and must produce
 * a uint64_t result that gets XORed into bench_sink.
 */
#define BENCH_RUN(impl, name, setup, code) do {                           \
    /* setup */                                                            \
    setup;                                                                 \
    /* warmup */                                                           \
    for (int _i = 0; _i < BENCH_WARMUP; _i++) {                          \
        uint64_t _r; (void)_r;                                            \
        code;                                                              \
        bench_sink ^= _r;                                                  \
    }                                                                      \
    /* measured runs */                                                     \
    for (int _rep = 0; _rep < BENCH_REPEATS; _rep++) {                    \
        uint64_t _start = now_ns();                                        \
        for (int _i = 0; _i < BENCH_ITERATIONS; _i++) {                   \
            uint64_t _r; (void)_r;                                         \
            code;                                                           \
            bench_sink ^= _r;                                              \
        }                                                                  \
        uint64_t _elapsed = now_ns() - _start;                            \
        double _ns_per_op = (double)_elapsed / BENCH_ITERATIONS;           \
        printf("BENCH\t%s\t%s\t%.3f\t%d\n",                              \
               impl, name, _ns_per_op, BENCH_ITERATIONS);                  \
        fflush(stdout);                                                    \
    }                                                                      \
} while (0)

/*
 * BENCH_RUN_VOID — variant for benchmarks that don't produce a uint64_t.
 * The 'code' block just runs; we use the loop counter as the sink value.
 */
#define BENCH_RUN_VOID(impl, name, setup, code) do {                      \
    setup;                                                                 \
    for (int _i = 0; _i < BENCH_WARMUP; _i++) { code; }                  \
    for (int _rep = 0; _rep < BENCH_REPEATS; _rep++) {                    \
        uint64_t _start = now_ns();                                        \
        for (int _i = 0; _i < BENCH_ITERATIONS; _i++) {                   \
            code;                                                          \
            bench_sink ^= (uint64_t)_i;                                    \
        }                                                                  \
        uint64_t _elapsed = now_ns() - _start;                            \
        double _ns_per_op = (double)_elapsed / BENCH_ITERATIONS;           \
        printf("BENCH\t%s\t%s\t%.3f\t%d\n",                              \
               impl, name, _ns_per_op, BENCH_ITERATIONS);                  \
        fflush(stdout);                                                    \
    }                                                                      \
} while (0)

/*
 * BENCH_RUN_N — variant with configurable iteration count.
 * Use for heavy benchmarks (e.g., session creation) where 10M iterations
 * is infeasible.
 */
#define BENCH_RUN_N(impl, name, iters, setup, code) do {                   \
    setup;                                                                 \
    int _warmup_n = (iters) / 10; if (_warmup_n < 1) _warmup_n = 1;      \
    for (int _i = 0; _i < _warmup_n; _i++) {                             \
        uint64_t _r; (void)_r;                                            \
        code;                                                              \
        bench_sink ^= _r;                                                  \
    }                                                                      \
    for (int _rep = 0; _rep < BENCH_REPEATS; _rep++) {                    \
        uint64_t _start = now_ns();                                        \
        for (int _i = 0; _i < (iters); _i++) {                           \
            uint64_t _r; (void)_r;                                         \
            code;                                                           \
            bench_sink ^= _r;                                              \
        }                                                                  \
        uint64_t _elapsed = now_ns() - _start;                            \
        double _ns_per_op = (double)_elapsed / (iters);                    \
        printf("BENCH\t%s\t%s\t%.3f\t%d\n",                              \
               impl, name, _ns_per_op, (iters));                           \
        fflush(stdout);                                                    \
    }                                                                      \
} while (0)

#endif /* BENCH_HARNESS_H */

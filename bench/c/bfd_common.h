/*
 * bfd_common.h — Shared BFD protocol definitions for cross-implementation benchmarks.
 *
 * Extracted from:
 *   - FRR bfdd:  struct bfd_pkt (bfd.h, bfd_packet.c)
 *   - BIRD BFD:  struct bfd_ctl_packet (proto/bfd/packets.c)
 *
 * These structures match the RFC 5880 Section 4.1 wire format exactly.
 * No dependency on FRR or BIRD libraries — standalone definitions.
 */
#ifndef BFD_COMMON_H
#define BFD_COMMON_H

#include <stdint.h>
#include <string.h>
#include <arpa/inet.h>
#include <stdlib.h>

/* RFC 5880 Section 4.1 — Protocol constants */
#define BFD_VERSION       1
#define BFD_PKT_LEN       24
#define BFD_MAX_PKT_LEN   64

/* BFD session states (RFC 5880 Section 4.1) */
#define BFD_STATE_ADMIN_DOWN  0
#define BFD_STATE_DOWN        1
#define BFD_STATE_INIT        2
#define BFD_STATE_UP          3

/* BFD diagnostic codes (RFC 5880 Section 4.1) */
#define BFD_DIAG_NONE             0
#define BFD_DIAG_TIME_EXPIRED     1
#define BFD_DIAG_ECHO_FAILED      2
#define BFD_DIAG_NEIGHBOR_DOWN    3
#define BFD_DIAG_ADMIN_DOWN       7

/* -----------------------------------------------------------------------
 * Bit manipulation macros — FRR style (from bfdd/bfd.h)
 * ----------------------------------------------------------------------- */

/* Byte 0: version(3 bits) | diag(5 bits) */
#define BFD_SETVER(field, val)   ((field) = (uint8_t)(((val) & 0x07) << 5) | ((field) & 0x1F))
#define BFD_GETVER(field)        (((field) >> 5) & 0x07)
#define BFD_SETDIAG(field, val)  ((field) = (uint8_t)((field) & 0xE0) | ((val) & 0x1F))
#define BFD_GETDIAG(field)       ((field) & 0x1F)

/* Byte 1: state(2 bits) | flags(6 bits: P F C A D M) */
#define BFD_SETSTATE(field, val) ((field) = (uint8_t)(((val) & 0x03) << 6) | ((field) & 0x3F))
#define BFD_GETSTATE(field)      (((field) >> 6) & 0x03)

/* -----------------------------------------------------------------------
 * FRR-style packet structure: struct bfd_pkt
 * From FRR bfdd/bfd.h — 24 bytes, network byte order for uint32 fields.
 * FRR pattern: stack-allocate, memset, fill field-by-field, htonl().
 * ----------------------------------------------------------------------- */
struct bfd_pkt_frr {
    uint8_t  diag;             /* version(3) | diag(5) */
    uint8_t  flags;            /* state(2) | P|F|C|A|D|M */
    uint8_t  detect_mult;
    uint8_t  len;
    uint32_t my_discr;         /* network byte order */
    uint32_t your_discr;       /* network byte order */
    uint32_t des_min_tx;       /* network byte order, microseconds */
    uint32_t req_min_rx;       /* network byte order, microseconds */
    uint32_t req_min_echo_rx;  /* network byte order, microseconds */
};

/* -----------------------------------------------------------------------
 * BIRD-style packet structure: struct bfd_ctl_packet
 * From BIRD proto/bfd/packets.c — identical wire layout.
 * BIRD pattern: pre-allocated sk->tbuf (64 bytes), cast, in-place fill.
 * ----------------------------------------------------------------------- */
struct bfd_ctl_packet {
    uint8_t  vdiag;
    uint8_t  flags;
    uint8_t  detect_mult;
    uint8_t  length;
    uint32_t snd_id;
    uint32_t rcv_id;
    uint32_t des_min_tx_int;
    uint32_t req_min_rx_int;
    uint32_t req_min_echo_rx_int;
};

/* BIRD helper inlines (from proto/bfd/packets.c) */
static inline uint8_t bfd_pack_vdiag(uint8_t version, uint8_t diag) {
    return (uint8_t)((version << 5) | (diag & 0x1F));
}

static inline uint8_t bfd_pack_flags(uint8_t state, uint8_t flags) {
    return (uint8_t)((state << 6) | (flags & 0x3F));
}

static inline uint8_t bfd_unpack_state(uint8_t flags) {
    return (flags >> 6) & 0x03;
}

/* -----------------------------------------------------------------------
 * Simulated session state for benchmarks.
 * Minimal subset of FRR's struct bfd_session / BIRD's struct bfd_session.
 * ----------------------------------------------------------------------- */
struct bench_session {
    uint8_t  state;
    uint8_t  remote_state;
    uint8_t  diag;
    uint8_t  detect_mult;
    uint8_t  flags;             /* C-bit, etc. */
    uint8_t  polling;
    uint32_t my_discr;
    uint32_t remote_discr;
    uint32_t desired_min_tx;    /* microseconds */
    uint32_t required_min_rx;   /* microseconds */
    uint32_t remote_min_rx;     /* microseconds */
    uint32_t remote_des_min_tx; /* microseconds */
    uint64_t xmt_TO;            /* transmit interval, microseconds */
};

/* Initialize a session with typical production values */
static inline void bench_session_init(struct bench_session *s) {
    memset(s, 0, sizeof(*s));
    s->state            = BFD_STATE_UP;
    s->remote_state     = BFD_STATE_UP;
    s->diag             = BFD_DIAG_NONE;
    s->detect_mult      = 3;
    s->my_discr         = 0xDEADBEEF;
    s->remote_discr     = 0xCAFEBABE;
    s->desired_min_tx   = 100000;   /* 100ms in microseconds */
    s->required_min_rx  = 100000;
    s->remote_min_rx    = 100000;
    s->remote_des_min_tx = 100000;
    s->xmt_TO           = 100000;
}

/* -----------------------------------------------------------------------
 * Shared FSM and timer helpers (used by both FRR and BIRD benchmarks)
 * ----------------------------------------------------------------------- */

/* Max of two uint32 values */
static inline uint32_t u32_max(uint32_t a, uint32_t b) {
    return a > b ? a : b;
}

/* -----------------------------------------------------------------------
 * Simple linear-probe hashmap: uint32_t discriminator → bench_session*
 * Used by SessionCreate/SessionDemux benchmarks.
 * Portable alternative to hsearch_r (glibc-only).
 * ----------------------------------------------------------------------- */
#define SESS_MAP_CAP 2048  /* must be power of 2, > max sessions */

struct sess_map_entry {
    uint32_t key;             /* discriminator (0 = empty slot) */
    struct bench_session *val;
};

struct sess_map {
    struct sess_map_entry buckets[SESS_MAP_CAP];
};

static inline void sess_map_init(struct sess_map *m) {
    memset(m, 0, sizeof(*m));
}

static inline void sess_map_insert(struct sess_map *m, uint32_t key,
                                    struct bench_session *val) {
    uint32_t idx = key & (SESS_MAP_CAP - 1);
    for (;;) {
        if (m->buckets[idx].key == 0 || m->buckets[idx].key == key) {
            m->buckets[idx].key = key;
            m->buckets[idx].val = val;
            return;
        }
        idx = (idx + 1) & (SESS_MAP_CAP - 1);
    }
}

static inline struct bench_session *sess_map_lookup(const struct sess_map *m,
                                                      uint32_t key) {
    uint32_t idx = key & (SESS_MAP_CAP - 1);
    for (;;) {
        if (m->buckets[idx].key == key) return m->buckets[idx].val;
        if (m->buckets[idx].key == 0)   return NULL;
        idx = (idx + 1) & (SESS_MAP_CAP - 1);
    }
}

#endif /* BFD_COMMON_H */

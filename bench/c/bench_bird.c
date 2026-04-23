/*
 * bench_bird.c — BFD benchmarks replicating BIRD BFD packet/FSM patterns.
 *
 * Replicates the exact code paths from BIRD's BFD implementation:
 *   - bfd_send_ctl()             in proto/bfd/packets.c:283-319 — TX: pre-allocated buffer
 *   - bfd_rx_hook()              in proto/bfd/packets.c         — RX: parse from sk->rbuf
 *   - bfd_session_process_ctl()  in proto/bfd/bfd.c:313-355     — FSM: if/else chain
 *
 * Key difference from FRR: BIRD pre-allocates a 64-byte socket buffer (sk->tbuf)
 * and fills it in-place on every send. No stack allocation, no memset per send.
 *
 * Build: gcc -O2 -std=c11 -march=native -o bench_bird bench_bird.c
 * Run:   ./bench_bird
 */

#include "bfd_common.h"
#include "bench_harness.h"

/* -----------------------------------------------------------------------
 * BIRD-style pre-allocated transmit buffer (simulates sk->tbuf, 64 bytes)
 * This buffer persists across sends — no stack allocation per iteration.
 * ----------------------------------------------------------------------- */
static uint8_t bird_tbuf[BFD_MAX_PKT_LEN];

/* -----------------------------------------------------------------------
 * BIRD-style Marshal — replicates bfd_send_ctl() pattern
 *
 * From packets.c:293-310:
 *   struct bfd_ctl_packet *pkt = (void *) sk->tbuf;
 *   pkt->vdiag = bfd_pack_vdiag(1, s->loc_diag);
 *   pkt->flags = bfd_pack_flags(s->loc_state, 0);
 *   ...
 *
 * No memset — fields are overwritten directly in the pre-allocated buffer.
 * ----------------------------------------------------------------------- */
static inline void bird_marshal(const struct bench_session *s,
                                 uint8_t *tbuf) {
    struct bfd_ctl_packet *pkt = (struct bfd_ctl_packet *)tbuf;
    pkt->vdiag = bfd_pack_vdiag(BFD_VERSION, s->diag);
    pkt->flags = bfd_pack_flags(s->state, s->polling ? 0x20 : 0);
    pkt->detect_mult = s->detect_mult;
    pkt->length = BFD_PKT_LEN;
    pkt->snd_id = htonl(s->my_discr);
    pkt->rcv_id = htonl(s->remote_discr);
    pkt->des_min_tx_int = htonl(s->desired_min_tx);
    pkt->req_min_rx_int = htonl(s->required_min_rx);
    pkt->req_min_echo_rx_int = 0;
}

/* -----------------------------------------------------------------------
 * BIRD-style Unmarshal — replicates bfd_rx_hook() parse path
 *
 * BIRD reads from sk->rbuf, also pre-allocated 64 bytes.
 * Cast + ntohl() extraction + validation.
 * ----------------------------------------------------------------------- */
struct bird_parsed {
    uint8_t  version;
    uint8_t  diag;
    uint8_t  state;
    uint8_t  detect_mult;
    uint8_t  length;
    uint32_t snd_id;
    uint32_t rcv_id;
    uint32_t des_min_tx_int;
    uint32_t req_min_rx_int;
};

static inline int bird_unmarshal(const uint8_t *buf, size_t buflen,
                                  struct bird_parsed *out) {
    if (buflen < BFD_PKT_LEN) return -1;

    const struct bfd_ctl_packet *pkt = (const struct bfd_ctl_packet *)buf;

    out->version    = (pkt->vdiag >> 5) & 0x07;
    out->diag       = pkt->vdiag & 0x1F;
    out->state      = bfd_unpack_state(pkt->flags);
    out->detect_mult = pkt->detect_mult;
    out->length     = pkt->length;
    out->snd_id     = ntohl(pkt->snd_id);
    out->rcv_id     = ntohl(pkt->rcv_id);
    out->des_min_tx_int = ntohl(pkt->des_min_tx_int);
    out->req_min_rx_int = ntohl(pkt->req_min_rx_int);

    /* BIRD validation */
    if (out->version != BFD_VERSION) return -1;
    if (out->detect_mult == 0) return -1;
    if (out->length < BFD_PKT_LEN) return -1;

    return 0;
}

/* -----------------------------------------------------------------------
 * BIRD-style FSM — replicates bfd_session_process_ctl()
 *
 * From bfd.c:313-355. BIRD uses if/else chain, not nested switch.
 * The pattern is: check loc_state, then compare rem_state.
 * ----------------------------------------------------------------------- */
static inline uint8_t bird_fsm_transition(uint8_t loc_state, uint8_t rem_state) {
    uint8_t next = 0;

    switch (loc_state) {
    case BFD_STATE_ADMIN_DOWN:
        return loc_state;

    case BFD_STATE_DOWN:
        if (rem_state == BFD_STATE_DOWN)
            next = BFD_STATE_INIT;
        else if (rem_state == BFD_STATE_INIT)
            next = BFD_STATE_UP;
        break;

    case BFD_STATE_INIT:
        if (rem_state == BFD_STATE_ADMIN_DOWN)
            next = BFD_STATE_DOWN;
        else if (rem_state >= BFD_STATE_INIT)
            next = BFD_STATE_UP;
        break;

    case BFD_STATE_UP:
        if (rem_state <= BFD_STATE_DOWN)
            next = BFD_STATE_DOWN;
        break;
    }

    return next ? next : loc_state;
}

/* -----------------------------------------------------------------------
 * BIRD-style Jitter — BIRD uses a similar random jitter pattern
 * From bfd.c: tx_timer uses (75-100)% range, same as RFC 5880 §6.8.7.
 * ----------------------------------------------------------------------- */
static inline uint64_t bird_jitter(uint64_t xmt_TO, uint8_t detect_mult) {
    int maxpercent = (detect_mult == 1) ? 16 : 26;
    return (xmt_TO * (uint64_t)(75 + (random() % maxpercent))) / 100;
}

/* -----------------------------------------------------------------------
 * RecvStateToEvent — map received BFD state to event
 * ----------------------------------------------------------------------- */
static inline uint8_t bird_recv_state_to_event(uint8_t recv_state) {
    switch (recv_state) {
    case BFD_STATE_ADMIN_DOWN: return 0;
    case BFD_STATE_DOWN:       return 1;
    case BFD_STATE_INIT:       return 2;
    case BFD_STATE_UP:         return 3;
    default:                   return 0;
    }
}

/* -----------------------------------------------------------------------
 * DetectionTimeCalc — detect_mult * max(req_min_rx, remote_des_min_tx)
 * ----------------------------------------------------------------------- */
static inline uint64_t bird_detection_time(const struct bench_session *s) {
    return (uint64_t)s->detect_mult * u32_max(s->required_min_rx,
                                               s->remote_des_min_tx);
}

/* -----------------------------------------------------------------------
 * CalcTxInterval — max(desired_min_tx, remote_min_rx)
 * ----------------------------------------------------------------------- */
static inline uint32_t bird_calc_tx_interval(const struct bench_session *s) {
    return u32_max(s->desired_min_tx, s->remote_min_rx);
}

/* ======================================================================= */
/* Main — run all benchmarks                                                */
/* ======================================================================= */

int main(void) {
    struct bench_session sess;
    bench_session_init(&sess);

    /* Seed random for jitter benchmarks */
    srandom(42);

    /* Pre-marshal a packet into bird_tbuf for unmarshal benchmarks */
    bird_marshal(&sess, bird_tbuf);

    /* --- Marshal (in-place, pre-allocated buffer) --- */
    BENCH_RUN("bird", "Marshal",
        { /* no extra setup */ },
        {
            bird_marshal(&sess, bird_tbuf);
            _r = *(uint64_t *)bird_tbuf;
        }
    );

    /* --- Unmarshal --- */
    BENCH_RUN("bird", "Unmarshal",
        { /* no extra setup */ },
        {
            struct bird_parsed p;
            bird_unmarshal(bird_tbuf, BFD_PKT_LEN, &p);
            _r = p.snd_id;
        }
    );

    /* --- RoundTrip --- */
    BENCH_RUN("bird", "RoundTrip",
        { /* no extra setup */ },
        {
            bird_marshal(&sess, bird_tbuf);
            struct bird_parsed p;
            bird_unmarshal(bird_tbuf, BFD_PKT_LEN, &p);
            _r = p.snd_id;
        }
    );

    /* --- FSM: Up receives Up (no transition) --- */
    BENCH_RUN("bird", "FSMUpRecvUp",
        { /* no extra setup */ },
        {
            _r = bird_fsm_transition(BFD_STATE_UP, BFD_STATE_UP);
        }
    );

    /* --- FSM: Down receives Down → Init --- */
    BENCH_RUN("bird", "FSMDownRecvDown",
        { /* no extra setup */ },
        {
            _r = bird_fsm_transition(BFD_STATE_DOWN, BFD_STATE_DOWN);
        }
    );

    /* --- Jitter calculation --- */
    BENCH_RUN("bird", "Jitter",
        { /* no extra setup */ },
        {
            _r = bird_jitter(sess.xmt_TO, sess.detect_mult);
        }
    );

    /* --- RecvStateToEvent --- */
    BENCH_RUN("bird", "RecvStateToEvent",
        { /* no extra setup */ },
        {
            _r = bird_recv_state_to_event(BFD_STATE_UP);
        }
    );

    /* --- DetectionTimeCalc --- */
    BENCH_RUN("bird", "DetectionTimeCalc",
        { /* no extra setup */ },
        {
            _r = bird_detection_time(&sess);
        }
    );

    /* --- CalcTxInterval --- */
    BENCH_RUN("bird", "CalcTxInterval",
        { /* no extra setup */ },
        {
            _r = bird_calc_tx_interval(&sess);
        }
    );

    /* --- FullTxPath: marshal + jitter (in-place buffer) --- */
    BENCH_RUN("bird", "FullTxPath",
        { /* no extra setup */ },
        {
            bird_marshal(&sess, bird_tbuf);
            uint64_t j = bird_jitter(sess.xmt_TO, sess.detect_mult);
            _r = *(uint64_t *)bird_tbuf ^ j;
        }
    );

    /* --- FullRxPath: unmarshal + recv_state_to_event + fsm_transition --- */
    BENCH_RUN("bird", "FullRxPath",
        { /* no extra setup */ },
        {
            struct bird_parsed p;
            bird_unmarshal(bird_tbuf, BFD_PKT_LEN, &p);
            uint8_t ev = bird_recv_state_to_event(p.state);
            (void)ev;
            _r = bird_fsm_transition(BFD_STATE_UP, p.state);
        }
    );

    /* --- FSM: Up with timer expired → Down --- */
    BENCH_RUN("bird", "FSMUpTimerExpired",
        { /* no extra setup */ },
        {
            uint8_t st = BFD_STATE_UP;
            if (st == BFD_STATE_INIT || st == BFD_STATE_UP)
                st = BFD_STATE_DOWN;
            _r = st;
        }
    );

    /* --- FSM: AdminDown ignores all received states --- */
    BENCH_RUN("bird", "FSMIgnored",
        { /* no extra setup */ },
        {
            _r = bird_fsm_transition(BFD_STATE_ADMIN_DOWN, BFD_STATE_UP);
        }
    );

    /* --- SessionCreate1000: allocate 1000 sessions + insert into hashmap --- */
    BENCH_RUN_N("bird", "SessionCreate1000", 10000,
        {
            /* no per-repeat setup beyond what's in the loop */
        },
        {
            struct bench_session sessions[1000];
            struct sess_map map;
            sess_map_init(&map);
            for (int k = 0; k < 1000; k++) {
                bench_session_init(&sessions[k]);
                sessions[k].my_discr = (uint32_t)(k + 1);
                sess_map_insert(&map, sessions[k].my_discr, &sessions[k]);
            }
            _r = (uint64_t)sessions[999].my_discr;
        }
    );

    /* --- SessionDemux1000: lookup in pre-created 1000-session hashmap --- */
    {
        struct bench_session demux_sessions[1000];
        struct sess_map demux_map;
        sess_map_init(&demux_map);
        for (int k = 0; k < 1000; k++) {
            bench_session_init(&demux_sessions[k]);
            demux_sessions[k].my_discr = (uint32_t)(k + 1);
            sess_map_insert(&demux_map, demux_sessions[k].my_discr,
                            &demux_sessions[k]);
        }
        BENCH_RUN("bird", "SessionDemux1000",
            { /* map already created */ },
            {
                uint32_t key = (uint32_t)((_i % 1000) + 1);
                struct bench_session *s = sess_map_lookup(&demux_map, key);
                _r = s ? s->my_discr : 0;
            }
        );
    }

    /* Print sink to prevent entire program optimization */
    fprintf(stderr, "sink: %lu\n", (unsigned long)bench_sink);
    return 0;
}

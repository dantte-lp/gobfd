/*
 * bench_frr.c — BFD benchmarks replicating FRR bfdd packet/FSM patterns.
 *
 * Replicates the exact code paths from FRR's bfdd implementation:
 *   - ptm_bfd_snd()       in bfd_packet.c:419  — TX: stack alloc + memset + fill
 *   - bfd_recv_cb()       in bfd_packet.c:855  — RX: cast buffer, ntohl() extract
 *   - bs_state_handler()  in bfd.c:1383        — FSM: nested switch dispatch
 *   - ptm_bfd_start_xmt_timer() in bfd.c:482  — jitter: (75 + random()%26) / 100
 *
 * Build: gcc -O2 -std=c11 -march=native -o bench_frr bench_frr.c
 * Run:   ./bench_frr
 */

#include "bfd_common.h"
#include "bench_harness.h"

/* -----------------------------------------------------------------------
 * FRR-style Marshal — replicates ptm_bfd_snd() pattern
 *
 * FRR rebuilds the entire packet from scratch on every send:
 * 1. Stack-allocate struct bfd_pkt
 * 2. memset to zero
 * 3. Fill field-by-field with bit macros and htonl()
 * ----------------------------------------------------------------------- */
static inline void frr_marshal(const struct bench_session *s,
                                struct bfd_pkt_frr *cp) {
    memset(cp, 0, sizeof(*cp));
    cp->diag = s->diag;
    BFD_SETVER(cp->diag, BFD_VERSION);
    cp->flags = 0;
    BFD_SETSTATE(cp->flags, s->state);
    if (s->polling) cp->flags |= 0x20; /* P bit */
    cp->detect_mult = s->detect_mult;
    cp->len = BFD_PKT_LEN;
    cp->my_discr = htonl(s->my_discr);
    cp->your_discr = htonl(s->remote_discr);
    cp->des_min_tx = htonl(s->desired_min_tx);
    cp->req_min_rx = htonl(s->required_min_rx);
    cp->req_min_echo_rx = 0;
}

/* -----------------------------------------------------------------------
 * FRR-style Unmarshal — replicates bfd_recv_cb() parse path
 *
 * FRR casts the receive buffer to struct bfd_pkt* and extracts fields
 * with ntohl(). Includes basic validation (version, length, detect_mult).
 * ----------------------------------------------------------------------- */
struct frr_parsed {
    uint8_t  version;
    uint8_t  diag;
    uint8_t  state;
    uint8_t  detect_mult;
    uint8_t  len;
    uint32_t my_discr;
    uint32_t your_discr;
    uint32_t des_min_tx;
    uint32_t req_min_rx;
};

static inline int frr_unmarshal(const uint8_t *buf, size_t buflen,
                                 struct frr_parsed *out) {
    if (buflen < BFD_PKT_LEN) return -1;

    const struct bfd_pkt_frr *cp = (const struct bfd_pkt_frr *)buf;

    out->version    = BFD_GETVER(cp->diag);
    out->diag       = BFD_GETDIAG(cp->diag);
    out->state      = BFD_GETSTATE(cp->flags);
    out->detect_mult = cp->detect_mult;
    out->len        = cp->len;
    out->my_discr   = ntohl(cp->my_discr);
    out->your_discr = ntohl(cp->your_discr);
    out->des_min_tx = ntohl(cp->des_min_tx);
    out->req_min_rx = ntohl(cp->req_min_rx);

    /* FRR validation checks (bfd_recv_cb lines 940-970) */
    if (out->version != BFD_VERSION) return -1;
    if (out->detect_mult == 0) return -1;
    if (out->len < BFD_PKT_LEN) return -1;

    return 0;
}

/* -----------------------------------------------------------------------
 * FRR-style FSM — replicates bs_state_handler() dispatch
 *
 * FRR uses a nested switch on (current_state, received_state).
 * Extracted from bfd.c:1383 — bs_down_handler, bs_init_handler, etc.
 * ----------------------------------------------------------------------- */
static inline uint8_t frr_fsm_transition(uint8_t current, uint8_t recv_state) {
    switch (current) {
    case BFD_STATE_ADMIN_DOWN:
        return current; /* discard all */

    case BFD_STATE_DOWN:
        /* bs_down_handler — bfd.c */
        switch (recv_state) {
        case BFD_STATE_ADMIN_DOWN:
            return current; /* RFC: discard */
        case BFD_STATE_DOWN:
            return BFD_STATE_INIT;
        case BFD_STATE_INIT:
            return BFD_STATE_UP;
        case BFD_STATE_UP:
            return current; /* stay Down */
        default:
            return current;
        }

    case BFD_STATE_INIT:
        /* bs_init_handler — bfd.c */
        switch (recv_state) {
        case BFD_STATE_ADMIN_DOWN:
            return BFD_STATE_DOWN;
        case BFD_STATE_DOWN:
            return current; /* stay Init */
        case BFD_STATE_INIT:
        case BFD_STATE_UP:
            return BFD_STATE_UP;
        default:
            return current;
        }

    case BFD_STATE_UP:
        /* bs_up_handler — bfd.c */
        switch (recv_state) {
        case BFD_STATE_ADMIN_DOWN:
        case BFD_STATE_DOWN:
            return BFD_STATE_DOWN;
        case BFD_STATE_INIT:
        case BFD_STATE_UP:
            return current; /* stay Up */
        default:
            return current;
        }

    default:
        return current;
    }
}

/* -----------------------------------------------------------------------
 * FRR-style Jitter — replicates ptm_bfd_start_xmt_timer()
 *
 * From bfd.c:496-497:
 *   maxpercent = (detect_mult == 1) ? 16 : 26;
 *   jittered = xmt_TO * (75 + (random() % maxpercent)) / 100;
 * ----------------------------------------------------------------------- */
static inline uint64_t frr_jitter(uint64_t xmt_TO, uint8_t detect_mult) {
    int maxpercent = (detect_mult == 1) ? 16 : 26;
    return (xmt_TO * (uint64_t)(75 + (random() % maxpercent))) / 100;
}

/* -----------------------------------------------------------------------
 * RecvStateToEvent — map received BFD state to FSM event
 * ----------------------------------------------------------------------- */
static inline uint8_t frr_recv_state_to_event(uint8_t recv_state) {
    switch (recv_state) {
    case BFD_STATE_ADMIN_DOWN: return 0; /* EventRecvAdminDown */
    case BFD_STATE_DOWN:       return 1; /* EventRecvDown */
    case BFD_STATE_INIT:       return 2; /* EventRecvInit */
    case BFD_STATE_UP:         return 3; /* EventRecvUp */
    default:                   return 0;
    }
}

/* -----------------------------------------------------------------------
 * DetectionTimeCalc — detect_mult * max(req_min_rx, remote_des_min_tx)
 * ----------------------------------------------------------------------- */
static inline uint64_t frr_detection_time(const struct bench_session *s) {
    return (uint64_t)s->detect_mult * u32_max(s->required_min_rx,
                                               s->remote_des_min_tx);
}

/* -----------------------------------------------------------------------
 * CalcTxInterval — max(desired_min_tx, remote_min_rx)
 * ----------------------------------------------------------------------- */
static inline uint32_t frr_calc_tx_interval(const struct bench_session *s) {
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

    /* Pre-marshal a packet for unmarshal benchmarks */
    struct bfd_pkt_frr wire_pkt;
    frr_marshal(&sess, &wire_pkt);
    uint8_t *wire = (uint8_t *)&wire_pkt;

    /* --- Marshal --- */
    BENCH_RUN("frr", "Marshal",
        { /* no extra setup */ },
        {
            struct bfd_pkt_frr cp;
            frr_marshal(&sess, &cp);
            _r = cp.my_discr;
        }
    );

    /* --- Unmarshal --- */
    BENCH_RUN("frr", "Unmarshal",
        { /* no extra setup */ },
        {
            struct frr_parsed p;
            frr_unmarshal(wire, BFD_PKT_LEN, &p);
            _r = p.my_discr;
        }
    );

    /* --- RoundTrip --- */
    BENCH_RUN("frr", "RoundTrip",
        { /* no extra setup */ },
        {
            struct bfd_pkt_frr cp;
            frr_marshal(&sess, &cp);
            struct frr_parsed p;
            frr_unmarshal((uint8_t *)&cp, BFD_PKT_LEN, &p);
            _r = p.my_discr;
        }
    );

    /* --- FSM: Up receives Up (no transition) --- */
    BENCH_RUN("frr", "FSMUpRecvUp",
        { /* no extra setup */ },
        {
            _r = frr_fsm_transition(BFD_STATE_UP, BFD_STATE_UP);
        }
    );

    /* --- FSM: Down receives Down → Init --- */
    BENCH_RUN("frr", "FSMDownRecvDown",
        { /* no extra setup */ },
        {
            _r = frr_fsm_transition(BFD_STATE_DOWN, BFD_STATE_DOWN);
        }
    );

    /* --- Jitter calculation --- */
    BENCH_RUN("frr", "Jitter",
        { /* no extra setup */ },
        {
            _r = frr_jitter(sess.xmt_TO, sess.detect_mult);
        }
    );

    /* --- RecvStateToEvent --- */
    BENCH_RUN("frr", "RecvStateToEvent",
        { /* no extra setup */ },
        {
            _r = frr_recv_state_to_event(BFD_STATE_UP);
        }
    );

    /* --- DetectionTimeCalc --- */
    BENCH_RUN("frr", "DetectionTimeCalc",
        { /* no extra setup */ },
        {
            _r = frr_detection_time(&sess);
        }
    );

    /* --- CalcTxInterval --- */
    BENCH_RUN("frr", "CalcTxInterval",
        { /* no extra setup */ },
        {
            _r = frr_calc_tx_interval(&sess);
        }
    );

    /* --- FullTxPath: marshal + jitter --- */
    BENCH_RUN("frr", "FullTxPath",
        { /* no extra setup */ },
        {
            struct bfd_pkt_frr cp;
            frr_marshal(&sess, &cp);
            uint64_t j = frr_jitter(sess.xmt_TO, sess.detect_mult);
            _r = cp.my_discr ^ j;
        }
    );

    /* --- FullRxPath: unmarshal + recv_state_to_event + fsm_transition --- */
    BENCH_RUN("frr", "FullRxPath",
        { /* no extra setup */ },
        {
            struct frr_parsed p;
            frr_unmarshal(wire, BFD_PKT_LEN, &p);
            uint8_t ev = frr_recv_state_to_event(p.state);
            (void)ev;
            _r = frr_fsm_transition(BFD_STATE_UP, p.state);
        }
    );

    /* --- FSM: Up with timer expired → Down --- */
    BENCH_RUN("frr", "FSMUpTimerExpired",
        { /* no extra setup */ },
        {
            uint8_t st = BFD_STATE_UP;
            if (st == BFD_STATE_INIT || st == BFD_STATE_UP)
                st = BFD_STATE_DOWN;
            _r = st;
        }
    );

    /* --- FSM: AdminDown ignores all received states --- */
    BENCH_RUN("frr", "FSMIgnored",
        { /* no extra setup */ },
        {
            _r = frr_fsm_transition(BFD_STATE_ADMIN_DOWN, BFD_STATE_UP);
        }
    );

    /* --- SessionCreate1000: allocate 1000 sessions + insert into hashmap --- */
    BENCH_RUN_N("frr", "SessionCreate1000", 10000,
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
        BENCH_RUN("frr", "SessionDemux1000",
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

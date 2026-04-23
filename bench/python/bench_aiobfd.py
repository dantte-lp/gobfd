#!/usr/bin/env python3
"""BFD benchmarks replicating aiobfd patterns.

Replicates the exact code patterns from the aiobfd library
(https://github.com/netedgeplus/aiobfd):

  - Marshal:  bitstring.pack(PACKET_FORMAT, **data)  — session.py:280
  - Unmarshal: bitstring.ConstBitStream(data).unpack(PACKET_FORMAT) — packet.py:76
  - FSM:      if/elif chain — session.py:373-406
  - Jitter:   random.uniform(0.75, 0.90) — session.py:316-321

Output format: BENCH<tab>impl<tab>name<tab>ns_per_op<tab>iterations
Compatible with scripts/gen-report.sh parser.
"""

import random
import time

import bitstring

# -----------------------------------------------------------------------
# Protocol constants — RFC 5880 Section 4.1
# -----------------------------------------------------------------------
STATE_ADMIN_DOWN = 0
STATE_DOWN = 1
STATE_INIT = 2
STATE_UP = 3

# -----------------------------------------------------------------------
# aiobfd packet format — from aiobfd/packet.py lines 31-48
# This format string is parsed on EVERY call to bitstring.pack().
# That parsing overhead is a significant part of the benchmark.
# -----------------------------------------------------------------------
PACKET_FORMAT = (
    "uint:3=version,"
    "uint:5=diag,"
    "uint:2=state,"
    "bool=poll,"
    "bool=final,"
    "bool=control_plane_independent,"
    "bool=authentication_present,"
    "bool=demand_mode,"
    "bool=multipoint,"
    "uint:8=detect_mult,"
    "uint:8=length,"
    "uint:32=my_discr,"
    "uint:32=your_discr,"
    "uint:32=desired_min_tx_interval,"
    "uint:32=required_min_rx_interval,"
    "uint:32=required_min_echo_rx_interval"
)

# -----------------------------------------------------------------------
# Benchmark parameters
# -----------------------------------------------------------------------
ITERATIONS = 100_000  # Python is ~100-1000x slower than C/Go
REPEATS = 6
IMPL = "python-aiobfd"

# Default session state matching GoBFD bench_test.go
SESSION_FIELDS = {
    "version": 1,
    "diag": 0,
    "state": STATE_UP,
    "poll": False,
    "final": False,
    "control_plane_independent": False,
    "authentication_present": False,
    "demand_mode": False,
    "multipoint": False,
    "detect_mult": 3,
    "length": 24,
    "my_discr": 0xDEADBEEF,
    "your_discr": 0xCAFEBABE,
    "desired_min_tx_interval": 100_000,
    "required_min_rx_interval": 100_000,
    "required_min_echo_rx_interval": 0,
}


def bench_run(name, func, iterations=ITERATIONS):
    """Run a benchmark function and print results in BENCH format."""
    # Warmup
    for _ in range(iterations // 10):
        func()

    for _ in range(REPEATS):
        start = time.monotonic_ns()
        for _ in range(iterations):
            func()
        elapsed = time.monotonic_ns() - start
        ns_per_op = elapsed / iterations
        print(f"BENCH\t{IMPL}\t{name}\t{ns_per_op:.3f}\t{iterations}", flush=True)


# -----------------------------------------------------------------------
# Marshal — bitstring.pack() per send (aiobfd pattern)
# -----------------------------------------------------------------------
def marshal_bitstring():
    """aiobfd pattern: bitstring.pack(PACKET_FORMAT, **fields).bytes"""
    return bitstring.pack(PACKET_FORMAT, **SESSION_FIELDS).bytes


# -----------------------------------------------------------------------
# Unmarshal — bitstring.ConstBitStream().unpack() per receive
# -----------------------------------------------------------------------
# Pre-marshal a wire packet for unmarshal benchmarks
WIRE_PACKET = marshal_bitstring()


def unmarshal_bitstring():
    """aiobfd pattern: ConstBitStream(data).unpack(PACKET_FORMAT)"""
    pkt = bitstring.ConstBitStream(WIRE_PACKET)
    return pkt.unpack(PACKET_FORMAT)


# -----------------------------------------------------------------------
# RoundTrip — marshal + unmarshal
# -----------------------------------------------------------------------
def roundtrip():
    """Marshal then immediately unmarshal."""
    wire = bitstring.pack(PACKET_FORMAT, **SESSION_FIELDS).bytes
    pkt = bitstring.ConstBitStream(wire)
    return pkt.unpack(PACKET_FORMAT)


# -----------------------------------------------------------------------
# FSM — if/elif chain from session.py:373-406
# -----------------------------------------------------------------------
def fsm_up_recv_up():
    """aiobfd FSM: Up receives Up → stay Up."""
    loc = STATE_UP
    rem = STATE_UP
    if loc == STATE_ADMIN_DOWN:
        return loc
    if rem == STATE_ADMIN_DOWN:
        if loc != STATE_DOWN:
            return STATE_DOWN
        return loc
    if loc == STATE_DOWN:
        if rem == STATE_DOWN:
            return STATE_INIT
        if rem == STATE_INIT:
            return STATE_UP
    elif loc == STATE_INIT:
        if rem in (STATE_INIT, STATE_UP):
            return STATE_UP
    elif loc == STATE_UP:
        if rem == STATE_DOWN:
            return STATE_DOWN
    return loc


def fsm_down_recv_down():
    """aiobfd FSM: Down receives Down → Init."""
    loc = STATE_DOWN
    rem = STATE_DOWN
    if loc == STATE_ADMIN_DOWN:
        return loc
    if rem == STATE_ADMIN_DOWN:
        if loc != STATE_DOWN:
            return STATE_DOWN
        return loc
    if loc == STATE_DOWN:
        if rem == STATE_DOWN:
            return STATE_INIT
        if rem == STATE_INIT:
            return STATE_UP
    elif loc == STATE_INIT:
        if rem in (STATE_INIT, STATE_UP):
            return STATE_UP
    elif loc == STATE_UP:
        if rem == STATE_DOWN:
            return STATE_DOWN
    return loc


# -----------------------------------------------------------------------
# Jitter — random.uniform from session.py:316-321
# -----------------------------------------------------------------------
XMT_TO = 100_000  # microseconds


def jitter():
    """aiobfd pattern: interval * random.uniform(0.75, 0.90) for detect_mult==1."""
    # For detect_mult > 1: interval * (1 - random.uniform(0, 0.25))
    return XMT_TO * (1 - random.uniform(0, 0.25))


# -----------------------------------------------------------------------
# RecvStateToEvent — map received state to event
# -----------------------------------------------------------------------
def recv_state_to_event():
    """Map received BFD state to FSM event."""
    s = STATE_UP
    if s == STATE_ADMIN_DOWN:
        return 0
    if s == STATE_DOWN:
        return 1
    if s == STATE_INIT:
        return 2
    if s == STATE_UP:
        return 3
    return 0


# -----------------------------------------------------------------------
# DetectionTimeCalc — detect_mult * max(req_min_rx, remote_des_min_tx)
# -----------------------------------------------------------------------
DETECT_MULT = 3
REQ_MIN_RX = 100_000
REMOTE_DES_MIN_TX = 100_000


def detection_time_calc():
    """Calculate detection time per RFC 5880."""
    return DETECT_MULT * max(REQ_MIN_RX, REMOTE_DES_MIN_TX)


# -----------------------------------------------------------------------
# CalcTxInterval — max(desired_min_tx, remote_min_rx)
# -----------------------------------------------------------------------
DESIRED_MIN_TX = 100_000
REMOTE_MIN_RX = 100_000


def calc_tx_interval():
    """Calculate negotiated TX interval."""
    return max(DESIRED_MIN_TX, REMOTE_MIN_RX)


# -----------------------------------------------------------------------
# FullTxPath — marshal + jitter
# -----------------------------------------------------------------------
def full_tx_path():
    """Marshal packet + calculate jitter."""
    wire = bitstring.pack(PACKET_FORMAT, **SESSION_FIELDS).bytes
    j = XMT_TO * (1 - random.uniform(0, 0.25))
    return wire, j


# -----------------------------------------------------------------------
# FullRxPath — unmarshal + recv_state_to_event + FSM
# -----------------------------------------------------------------------
def full_rx_path():
    """Unmarshal packet + map state + FSM transition."""
    pkt = bitstring.ConstBitStream(WIRE_PACKET)
    fields = pkt.unpack(PACKET_FORMAT)
    rem_state = fields[2]  # state field
    # recv_state_to_event (inline)
    _ = {STATE_ADMIN_DOWN: 0, STATE_DOWN: 1, STATE_INIT: 2, STATE_UP: 3}.get(
        rem_state, 0
    )
    # FSM transition (Up receives rem_state)
    loc = STATE_UP
    if rem_state in (STATE_ADMIN_DOWN, STATE_DOWN):
        return STATE_DOWN
    return loc


# -----------------------------------------------------------------------
# FSM: Up with timer expired → Down
# -----------------------------------------------------------------------
def fsm_up_timer_expired():
    """Timer expiry: Up/Init → Down."""
    st = STATE_UP
    if st in (STATE_INIT, STATE_UP):
        st = STATE_DOWN
    return st


# -----------------------------------------------------------------------
# FSM: AdminDown ignores all
# -----------------------------------------------------------------------
def fsm_ignored():
    """AdminDown discards all received states."""
    loc = STATE_ADMIN_DOWN
    _ = STATE_UP  # received state (discarded by AdminDown)
    if loc == STATE_ADMIN_DOWN:
        return loc
    return loc


# -----------------------------------------------------------------------
# SessionCreate1000 — create 1000 session dicts
# -----------------------------------------------------------------------
def session_create_1000():
    """Create 1000 session dicts and insert into a lookup dict."""
    sessions = {}
    for k in range(1000):
        s = {
            "state": STATE_UP,
            "remote_state": STATE_UP,
            "diag": 0,
            "detect_mult": 3,
            "my_discr": k + 1,
            "remote_discr": 0xCAFEBABE,
            "desired_min_tx": 100_000,
            "required_min_rx": 100_000,
            "remote_min_rx": 100_000,
        }
        sessions[k + 1] = s
    return sessions


# -----------------------------------------------------------------------
# SessionDemux1000 — lookup in pre-created 1000-session dict
# -----------------------------------------------------------------------
_DEMUX_SESSIONS = session_create_1000()


def session_demux_1000():
    """Lookup a session by discriminator in a 1000-entry dict."""
    key = random.randint(1, 1000)
    return _DEMUX_SESSIONS[key]


# ======================================================================
# Main
# ======================================================================
if __name__ == "__main__":
    bench_run("Marshal", marshal_bitstring)
    bench_run("Unmarshal", unmarshal_bitstring)
    bench_run("RoundTrip", roundtrip)
    bench_run("FSMUpRecvUp", fsm_up_recv_up)
    bench_run("FSMDownRecvDown", fsm_down_recv_down)
    bench_run("Jitter", jitter)
    bench_run("RecvStateToEvent", recv_state_to_event)
    bench_run("DetectionTimeCalc", detection_time_calc)
    bench_run("CalcTxInterval", calc_tx_interval)
    bench_run("FullTxPath", full_tx_path)
    bench_run("FullRxPath", full_rx_path)
    bench_run("FSMUpTimerExpired", fsm_up_timer_expired)
    bench_run("FSMIgnored", fsm_ignored)
    bench_run("SessionCreate1000", session_create_1000, iterations=1000)
    bench_run("SessionDemux1000", session_demux_1000)

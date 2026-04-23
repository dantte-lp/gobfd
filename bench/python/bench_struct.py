#!/usr/bin/env python3
"""BFD benchmarks using Python's struct module (optimized baseline).

Shows the best possible Python performance for BFD packet codec
without bitstring overhead. Uses struct.pack()/struct.unpack() which
compile the format string once internally.

This serves as a comparison point: how much of Python's overhead is
inherent vs. caused by bitstring's format string parsing on every call.

Output format: BENCH<tab>impl<tab>name<tab>ns_per_op<tab>iterations
"""

import random
import struct
import time

# -----------------------------------------------------------------------
# Protocol constants
# -----------------------------------------------------------------------
STATE_ADMIN_DOWN = 0
STATE_DOWN = 1
STATE_INIT = 2
STATE_UP = 3

# -----------------------------------------------------------------------
# BFD packet format for struct.pack/unpack
# >BBBBIIII = big-endian: 4 bytes header + 5 x uint32 = 24 bytes
# -----------------------------------------------------------------------
BFD_STRUCT_FORMAT = ">BBBBIIII"
BFD_STRUCT_SIZE = struct.calcsize(BFD_STRUCT_FORMAT)

# Pre-computed header bytes for session state:
#   byte0 = version(3 bits=1) | diag(5 bits=0) = 0x20
#   byte1 = state(2 bits=Up=3) | flags(6 bits=0) = 0xC0
BYTE0 = (1 << 5) | 0       # version=1, diag=0 → 0x20
BYTE1 = (STATE_UP << 6)    # state=Up, no flags → 0xC0
DETECT_MULT = 3
PKT_LEN = 24
MY_DISCR = 0xDEADBEEF
YOUR_DISCR = 0xCAFEBABE
DES_MIN_TX = 100_000
REQ_MIN_RX = 100_000

# -----------------------------------------------------------------------
# Benchmark parameters
# -----------------------------------------------------------------------
ITERATIONS = 100_000
REPEATS = 6
IMPL = "python-struct"


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
# Marshal — struct.pack()
# -----------------------------------------------------------------------
def marshal_struct():
    """Optimized Python: struct.pack() with pre-computed byte values."""
    return struct.pack(
        BFD_STRUCT_FORMAT,
        BYTE0, BYTE1, DETECT_MULT, PKT_LEN,
        MY_DISCR, YOUR_DISCR, DES_MIN_TX, REQ_MIN_RX,
    )


# Pre-marshal for unmarshal benchmarks
WIRE_PACKET = marshal_struct()


# -----------------------------------------------------------------------
# Unmarshal — struct.unpack()
# -----------------------------------------------------------------------
def unmarshal_struct():
    """Optimized Python: struct.unpack() from bytes."""
    return struct.unpack(BFD_STRUCT_FORMAT, WIRE_PACKET)


# -----------------------------------------------------------------------
# RoundTrip
# -----------------------------------------------------------------------
def roundtrip():
    wire = struct.pack(
        BFD_STRUCT_FORMAT,
        BYTE0, BYTE1, DETECT_MULT, PKT_LEN,
        MY_DISCR, YOUR_DISCR, DES_MIN_TX, REQ_MIN_RX,
    )
    return struct.unpack(BFD_STRUCT_FORMAT, wire)


# -----------------------------------------------------------------------
# FSM — same if/elif chain as bench_aiobfd.py
# -----------------------------------------------------------------------
def fsm_up_recv_up():
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
# Jitter
# -----------------------------------------------------------------------
XMT_TO = 100_000


def jitter():
    return XMT_TO * (1 - random.uniform(0, 0.25))


# -----------------------------------------------------------------------
# RecvStateToEvent
# -----------------------------------------------------------------------
def recv_state_to_event():
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
# DetectionTimeCalc
# -----------------------------------------------------------------------
def detection_time_calc():
    return DETECT_MULT * max(REQ_MIN_RX, DES_MIN_TX)


# -----------------------------------------------------------------------
# CalcTxInterval
# -----------------------------------------------------------------------
REMOTE_MIN_RX = 100_000


def calc_tx_interval():
    return max(DES_MIN_TX, REMOTE_MIN_RX)


# -----------------------------------------------------------------------
# FullTxPath
# -----------------------------------------------------------------------
def full_tx_path():
    wire = struct.pack(
        BFD_STRUCT_FORMAT,
        BYTE0, BYTE1, DETECT_MULT, PKT_LEN,
        MY_DISCR, YOUR_DISCR, DES_MIN_TX, REQ_MIN_RX,
    )
    j = XMT_TO * (1 - random.uniform(0, 0.25))
    return wire, j


# -----------------------------------------------------------------------
# FullRxPath — unmarshal + recv_state_to_event + FSM
# -----------------------------------------------------------------------
def full_rx_path():
    """Unmarshal packet + map state + FSM transition."""
    fields = struct.unpack(BFD_STRUCT_FORMAT, WIRE_PACKET)
    rem_state = (fields[1] >> 6) & 0x03  # extract state from byte1
    # FSM transition (Up receives rem_state)
    loc = STATE_UP
    if rem_state in (STATE_ADMIN_DOWN, STATE_DOWN):
        return STATE_DOWN
    return loc


# -----------------------------------------------------------------------
# FSM: Up with timer expired → Down
# -----------------------------------------------------------------------
def fsm_up_timer_expired():
    st = STATE_UP
    if st in (STATE_INIT, STATE_UP):
        st = STATE_DOWN
    return st


# -----------------------------------------------------------------------
# FSM: AdminDown ignores all
# -----------------------------------------------------------------------
def fsm_ignored():
    loc = STATE_ADMIN_DOWN
    if loc == STATE_ADMIN_DOWN:
        return loc
    return loc


# -----------------------------------------------------------------------
# SessionCreate1000 — create 1000 session dicts
# -----------------------------------------------------------------------
def session_create_1000():
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
    key = random.randint(1, 1000)
    return _DEMUX_SESSIONS[key]


# ======================================================================
# Main
# ======================================================================
if __name__ == "__main__":
    bench_run("Marshal", marshal_struct)
    bench_run("Unmarshal", unmarshal_struct)
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

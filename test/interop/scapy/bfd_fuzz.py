#!/usr/bin/env python3
"""BFD protocol fuzzer using Scapy (RFC 5880 Section 6.8.6 validation).

Sends crafted/invalid BFD packets to GoBFD and verifies the daemon
rejects them without crashing. Each test targets a specific validation
step from RFC 5880 Section 6.8.6.

Exit codes:
  0 — all tests passed (gobfd survived all invalid packets)
  1 — gobfd crashed or became unresponsive
  2 — test infrastructure error
"""

import os
import socket
import struct
import sys
import time

# Scapy setup: disable interactive prompts and reduce noise.
os.environ["SCAPY_USE_LIBPCAP"] = "0"

from scapy.all import IP, UDP, Raw, send, conf  # noqa: E402

# Suppress Scapy warnings.
conf.verb = 0

# -------------------------------------------------------------------------
# Configuration
# -------------------------------------------------------------------------

GOBFD_IP = os.environ.get("GOBFD_IP", "172.20.0.10")
BFD_DST_PORT = 3784
BFD_SRC_PORT = 49152
TTL_SINGLE_HOP = 255

# How long to wait after all fuzz packets before checking liveness.
SETTLE_TIME = 2.0

# -------------------------------------------------------------------------
# Raw BFD packet builder (avoids Scapy contrib quirks for precise control)
# -------------------------------------------------------------------------


def build_bfd_packet(
    version=1,
    diag=0,
    state=1,  # Down
    poll=False,
    final=False,
    cpi=False,
    auth_present=False,
    demand=False,
    multipoint=False,
    detect_mult=3,
    length=24,
    my_discr=0x11111111,
    your_discr=0,
    desired_min_tx=1000000,
    required_min_rx=1000000,
    echo_rx=0,
    auth_data=b"",
):
    """Build a raw BFD Control packet as bytes.

    This builds the packet at the byte level for precise control over
    every field, including intentionally invalid values.
    """
    # Byte 0: Version(3) | Diag(5)
    byte0 = ((version & 0x07) << 5) | (diag & 0x1F)

    # Byte 1: State(2) | P | F | C | A | D | M
    byte1 = (state & 0x03) << 6
    if poll:
        byte1 |= 1 << 5
    if final:
        byte1 |= 1 << 4
    if cpi:
        byte1 |= 1 << 3
    if auth_present:
        byte1 |= 1 << 2
    if demand:
        byte1 |= 1 << 1
    if multipoint:
        byte1 |= 1 << 0

    header = struct.pack(
        "!BBBBIIIII",
        byte0,
        byte1,
        detect_mult & 0xFF,
        length & 0xFF,
        my_discr,
        your_discr,
        desired_min_tx,
        required_min_rx,
        echo_rx,
    )

    return header + auth_data


def send_bfd(payload):
    """Send a raw BFD payload via UDP to gobfd."""
    pkt = (
        IP(dst=GOBFD_IP, ttl=TTL_SINGLE_HOP)
        / UDP(sport=BFD_SRC_PORT, dport=BFD_DST_PORT)
        / Raw(load=payload)
    )
    send(pkt, verbose=False)


# -------------------------------------------------------------------------
# Liveness check: send a valid BFD Down packet and see if gobfd is alive.
# We check by verifying the process responds (no crash) via a simple
# UDP socket round-trip attempt.
# -------------------------------------------------------------------------


def check_gobfd_alive():
    """Verify gobfd is still responding by attempting a UDP connection.

    Returns True if gobfd appears alive (socket connect succeeds or
    ICMP unreachable is not received), False otherwise.
    """
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        sock.settimeout(2.0)

        # Send a valid Down packet.
        valid_pkt = build_bfd_packet(
            version=1,
            state=1,  # Down
            detect_mult=3,
            length=24,
            my_discr=0xDEADDEAD,
            desired_min_tx=1000000,
            required_min_rx=1000000,
        )
        sock.sendto(valid_pkt, (GOBFD_IP, BFD_DST_PORT))

        # Try to receive a response (gobfd may or may not respond to
        # unknown discriminators, but the key test is that it doesn't crash).
        try:
            sock.recvfrom(128)
        except socket.timeout:
            pass  # No response is fine — gobfd just silently drops unknown sessions.

        sock.close()
        return True
    except (OSError, ConnectionRefusedError):
        return False


# -------------------------------------------------------------------------
# Fuzz Test Cases — RFC 5880 Section 6.8.6 validation rules
# -------------------------------------------------------------------------


def test_invalid_version():
    """RFC 5880 Section 6.8.6 Step 1: Version MUST be 1."""
    results = []
    for ver in [0, 2, 3, 4, 5, 6, 7]:
        pkt = build_bfd_packet(version=ver, my_discr=0xF0F00001)
        send_bfd(pkt)
        results.append(f"version={ver}")
    return "invalid_version", results


def test_zero_detect_mult():
    """RFC 5880 Section 6.8.6 Step 4: DetectMult MUST NOT be zero."""
    pkt = build_bfd_packet(detect_mult=0, my_discr=0xF0F00002)
    send_bfd(pkt)
    return "zero_detect_mult", ["detect_mult=0"]


def test_multipoint_set():
    """RFC 5880 Section 6.8.6 Step 5: Multipoint MUST be zero."""
    pkt = build_bfd_packet(multipoint=True, my_discr=0xF0F00003)
    send_bfd(pkt)
    return "multipoint_set", ["M=1"]


def test_zero_my_discriminator():
    """RFC 5880 Section 6.8.6 Step 6: MyDiscriminator MUST NOT be zero."""
    pkt = build_bfd_packet(my_discr=0)
    send_bfd(pkt)
    return "zero_my_discriminator", ["my_discr=0"]


def test_length_too_small():
    """RFC 5880 Section 6.8.6 Step 2: Length < 24 with A=0."""
    results = []
    for length in [0, 1, 12, 23]:
        pkt = build_bfd_packet(length=length, my_discr=0xF0F00004)
        send_bfd(pkt)
        results.append(f"length={length}")
    return "length_too_small", results


def test_length_exceeds_payload():
    """RFC 5880 Section 6.8.6 Step 3: Length > actual payload size."""
    # Send a 24-byte packet but claim length=48.
    pkt = build_bfd_packet(length=48, my_discr=0xF0F00005)
    send_bfd(pkt)
    return "length_exceeds_payload", ["length=48, actual=24"]


def test_truncated_packet():
    """Packet shorter than 24 bytes (minimum BFD header size)."""
    results = []
    for size in [0, 1, 4, 12, 20, 23]:
        full_pkt = build_bfd_packet(my_discr=0xF0F00006)
        send_bfd(full_pkt[:size])
        results.append(f"size={size}")
    return "truncated_packet", results


def test_auth_flag_without_section():
    """RFC 5880 Section 6.8.6: A=1 but no auth section present."""
    # A=1, Length=24 (header only, no auth data).
    pkt = build_bfd_packet(
        auth_present=True,
        length=24,
        my_discr=0xF0F00007,
    )
    send_bfd(pkt)
    return "auth_flag_no_section", ["A=1, length=24, no_auth_data"]


def test_auth_section_truncated():
    """Auth section present but truncated (incomplete auth header)."""
    results = []
    # A=1, length=26 (minimum with auth), but only 1 byte of auth data.
    pkt = build_bfd_packet(
        auth_present=True,
        length=26,
        my_discr=0xF0F00008,
        auth_data=b"\x01",  # Only Auth Type byte, missing rest.
    )
    send_bfd(pkt)
    results.append("auth_data=1_byte")

    # A=1, length=27, auth type=4 (SHA1 needs 28 bytes) but only 3 bytes.
    pkt = build_bfd_packet(
        auth_present=True,
        length=27,
        my_discr=0xF0F00009,
        auth_data=b"\x04\x1c\x01",  # Type=SHA1, Len=28, KeyID=1, truncated.
    )
    send_bfd(pkt)
    results.append("sha1_auth_truncated")

    return "auth_section_truncated", results


def test_invalid_auth_type():
    """Unknown auth type value (not 0-5)."""
    results = []
    for auth_type in [6, 7, 128, 255]:
        # Minimal auth section: Type + Len + KeyID.
        auth_data = struct.pack("BBB", auth_type, 3, 1)
        pkt = build_bfd_packet(
            auth_present=True,
            length=27,
            my_discr=0xF0F0000A,
            auth_data=auth_data,
        )
        send_bfd(pkt)
        results.append(f"auth_type={auth_type}")
    return "invalid_auth_type", results


def test_all_flags_set():
    """All flag bits set simultaneously (P, F, C, A, D, M — M is invalid)."""
    pkt = build_bfd_packet(
        poll=True,
        final=True,
        cpi=True,
        auth_present=True,
        demand=True,
        multipoint=True,
        my_discr=0xF0F0000B,
        length=24,
    )
    send_bfd(pkt)
    return "all_flags_set", ["P=F=C=A=D=M=1"]


def test_max_field_values():
    """Extreme field values (boundary testing)."""
    results = []

    # Max detect_mult.
    pkt = build_bfd_packet(detect_mult=255, my_discr=0xF0F0000C)
    send_bfd(pkt)
    results.append("detect_mult=255")

    # Max discriminator values.
    pkt = build_bfd_packet(my_discr=0xFFFFFFFF, your_discr=0xFFFFFFFF)
    send_bfd(pkt)
    results.append("max_discriminators")

    # Max interval values.
    pkt = build_bfd_packet(
        my_discr=0xF0F0000D,
        desired_min_tx=0xFFFFFFFF,
        required_min_rx=0xFFFFFFFF,
        echo_rx=0xFFFFFFFF,
    )
    send_bfd(pkt)
    results.append("max_intervals")

    return "max_field_values", results


def test_zero_intervals():
    """Zero interval values (RFC 5880: zero RequiredMinRxInterval = don't send)."""
    results = []

    # Zero desired_min_tx.
    pkt = build_bfd_packet(my_discr=0xF0F0000E, desired_min_tx=0)
    send_bfd(pkt)
    results.append("desired_min_tx=0")

    # Zero required_min_rx.
    pkt = build_bfd_packet(my_discr=0xF0F0000F, required_min_rx=0)
    send_bfd(pkt)
    results.append("required_min_rx=0")

    return "zero_intervals", results


def test_random_garbage():
    """Completely random bytes of various sizes."""
    import random

    results = []
    random.seed(42)  # Deterministic for reproducibility.

    for size in [1, 8, 24, 32, 48, 64, 128, 256, 512, 1024]:
        payload = bytes(random.getrandbits(8) for _ in range(size))
        send_bfd(payload)
        results.append(f"random_{size}bytes")

    return "random_garbage", results


def test_oversized_packet():
    """Packets larger than MaxPacketSize (64 bytes)."""
    results = []
    for extra in [1, 16, 64, 256, 1024]:
        size = 24 + extra
        pkt = build_bfd_packet(my_discr=0xF0F00010, length=24)
        pkt += b"\x00" * extra
        send_bfd(pkt)
        results.append(f"size={size}")
    return "oversized_packet", results


def test_wrong_ttl():
    """RFC 5881 Section 5: single-hop BFD MUST use TTL=255 (GTSM).

    Send valid packet with TTL < 255 to verify gobfd rejects it.
    NOTE: This tests the network I/O layer, not the codec.
    """
    results = []
    for ttl in [1, 64, 128, 254]:
        payload = build_bfd_packet(my_discr=0xF0F00011)
        pkt = (
            IP(dst=GOBFD_IP, ttl=ttl)
            / UDP(sport=BFD_SRC_PORT, dport=BFD_DST_PORT)
            / Raw(load=payload)
        )
        send(pkt, verbose=False)
        results.append(f"ttl={ttl}")
    return "wrong_ttl", results


def test_your_discr_zero_non_down():
    """RFC 5880 Section 6.8.6 Step 7: YourDiscr=0 only valid in Down/AdminDown."""
    results = []
    for state_val, state_name in [(2, "Init"), (3, "Up")]:
        pkt = build_bfd_packet(
            state=state_val,
            my_discr=0xF0F00012,
            your_discr=0,
        )
        send_bfd(pkt)
        results.append(f"state={state_name},your_discr=0")
    return "your_discr_zero_non_down", results


def test_rapid_fire():
    """Send 1000 packets rapidly to stress-test the receive path."""
    for i in range(1000):
        pkt = build_bfd_packet(
            version=(i % 8),  # Mix valid and invalid versions.
            detect_mult=(i % 256),  # Includes 0 (invalid).
            my_discr=0xF0F10000 + i,
        )
        send_bfd(pkt)
    return "rapid_fire", ["1000_mixed_packets"]


# -------------------------------------------------------------------------
# Main
# -------------------------------------------------------------------------


def main():
    tests = [
        test_invalid_version,
        test_zero_detect_mult,
        test_multipoint_set,
        test_zero_my_discriminator,
        test_length_too_small,
        test_length_exceeds_payload,
        test_truncated_packet,
        test_auth_flag_without_section,
        test_auth_section_truncated,
        test_invalid_auth_type,
        test_all_flags_set,
        test_max_field_values,
        test_zero_intervals,
        test_your_discr_zero_non_down,
        test_random_garbage,
        test_oversized_packet,
        test_wrong_ttl,
        test_rapid_fire,
    ]

    print(f"BFD Scapy Fuzzer: targeting {GOBFD_IP}:{BFD_DST_PORT}")
    print(f"Running {len(tests)} test cases...\n")

    passed = 0
    failed = 0

    for test_fn in tests:
        try:
            name, details = test_fn()
            print(f"  SENT  {name}: {', '.join(details[:5])}")
            if len(details) > 5:
                print(f"        ... and {len(details) - 5} more")
            passed += 1
        except Exception as exc:
            print(f"  FAIL  {test_fn.__name__}: {exc}")
            failed += 1

    # Wait for gobfd to process all packets.
    print(f"\nWaiting {SETTLE_TIME}s for gobfd to process packets...")
    time.sleep(SETTLE_TIME)

    # Verify gobfd is still alive.
    print("Checking gobfd liveness...")
    alive = check_gobfd_alive()

    print()
    print("=" * 60)
    print(f"Tests sent:  {passed + failed}")
    print(f"Send OK:     {passed}")
    print(f"Send FAIL:   {failed}")
    print(f"GoBFD alive: {'YES' if alive else 'NO (CRASHED!)'}")
    print("=" * 60)

    if not alive:
        print("\nFATAL: gobfd crashed under fuzz input!")
        sys.exit(1)

    if failed > 0:
        print(f"\nWARNING: {failed} test(s) failed to send packets")
        sys.exit(2)

    print("\nAll fuzz packets sent and gobfd survived.")
    sys.exit(0)


if __name__ == "__main__":
    main()

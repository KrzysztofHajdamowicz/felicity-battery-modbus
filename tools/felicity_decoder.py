#!/usr/bin/env python3
"""
Felicity Solar battery (Voltronic "LIB" BMS protocol over RS485) - reference decoder.

Builds requests, validates the non-standard responses (16-bit register count instead of the
Modbus byte count), maps registers to values and derives the usual metrics. Also parses the bus
captures in ../captures/ (lines: "<time> TX|RX <hex bytes>").

Usage:
  python3 felicity_decoder.py --selftest
  python3 felicity_decoder.py ../captures/2026-10-10-rs485-bus.log
  python3 felicity_decoder.py --request 1 37 17        # request frame for address 1, regs 37..53
"""
from __future__ import annotations
import sys
from dataclasses import dataclass, field
from decimal import Decimal, ROUND_HALF_UP

# ---------------------------------------------------------------- Modbus RTU layer


def crc16(data: bytes) -> int:
    crc = 0xFFFF
    for b in data:
        crc ^= b
        for _ in range(8):
            crc = (crc >> 1) ^ 0xA001 if crc & 1 else crc >> 1
    return crc


def build_request(address: int, start: int, count: int, fc: int = 3) -> bytes:
    """<<addr, fc, start:16, count:16>> + CRC (little-endian)."""
    assert 0 <= start <= 0xFFFF and 1 <= count <= 0x7D
    pdu = bytes([address, fc]) + start.to_bytes(2, "big") + count.to_bytes(2, "big")
    return pdu + crc16(pdu).to_bytes(2, "little")


class FrameError(Exception):
    pass


def parse_response(frame: bytes) -> tuple[int, int, list[int]]:
    """Return (address, function, [u16 registers]).

    Accepts three header variants after <addr, fc>:
      a) 1 byte  = number of data bytes            (standard Modbus),
      b) 1 byte  = number of data bytes + 2,
      c) 2 bytes = number of REGISTERS, big-endian (Felicity / Voltronic document).
    """
    if len(frame) < 3:
        raise FrameError("No response" if not frame else "Invalid data")
    if crc16(frame[:-2]) != int.from_bytes(frame[-2:], "little"):
        raise FrameError("CRC error")
    addr, fc = frame[0], frame[1]
    if fc & 0x80:
        raise FrameError(f"Exception code {frame[2]}")
    body = frame[2:-2]
    if len(body) < 2:
        raise FrameError("Blank pack")
    n8, rest8 = body[0], body[1:]
    if n8 == len(rest8) or n8 == len(rest8) + 2:          # a), b)
        data = rest8
    else:
        n16, rest16 = int.from_bytes(body[0:2], "big"), body[2:]
        if n16 * 2 != len(rest16):                        # c)
            raise FrameError("Invalid payload")
        data = rest16
    regs = [int.from_bytes(data[i:i + 2], "big") for i in range(0, len(data) - len(data) % 2, 2)]
    return addr, fc, regs


def expected_response_length(count: int) -> int:
    """Frame length for the Felicity header variant."""
    return 4 + 2 * count + 2

# ---------------------------------------------------------------- register map


def s16(v: int) -> int:
    return v - 0x10000 if v & 0x8000 else v


def _round_half_up(x: float, digits: int) -> float:
    q = Decimal(1).scaleb(-digits)
    return float(Decimal(str(x)).quantize(q, rounding=ROUND_HALF_UP))


@dataclass
class Pack:
    address: int
    cell_mult: float = 0.001          # FLB48314TG1: mV. Document: 0.1 V
    voltage_mult: float = 0.001       # FLB48314TG1: mV. Document: 0.1 V
    cell_count: int | None = None
    cells: dict[int, float] = field(default_factory=dict)       # 1-based -> V
    temp_sensor_count: int | None = None
    temps_raw: dict[int, int] = field(default_factory=dict)     # 1-based -> raw (0.1 K)
    charge_current: float | None = None
    discharge_current: float | None = None
    voltage: float | None = None
    soc: int | None = None
    capacity_ah: float | None = None
    # document-only registers (unverified on Felicity)
    packs_online: int | None = None
    charge_alarm: int | None = None
    discharge_alarm: int | None = None
    charge_protect: int | None = None
    charge_protect2: int | None = None
    discharge_protect: int | None = None
    discharge_protect2: int | None = None
    design_capacity_ah: float | None = None
    run_time_to_empty_min: int | None = None
    remaining_capacity_ah: float | None = None
    # limits / status
    charge_voltage_limit: float | None = None
    discharge_voltage_limit: float | None = None
    max_charge_current: float | None = None
    max_discharge_current: float | None = None
    status: int | None = None

    # ---- derived
    @property
    def current(self) -> float | None:
        if self.charge_current is None or self.discharge_current is None:
            return None
        return round(self.charge_current - abs(self.discharge_current), 1)

    @property
    def power(self) -> int | None:
        if self.current is None or self.voltage is None:
            return None
        return round(self.current * self.voltage)

    def temp_c(self, n: int) -> float | None:
        raw = self.temps_raw.get(n)
        if not raw:
            return None
        tenths = raw - 2731.5 if raw > 1000 else raw * 10 - 2731.5
        return _round_half_up(tenths / 10, 1)

    @property
    def temperature(self) -> float | None:
        ts = [self.temp_c(i) for i in (1, 2, 3) if self.temps_raw.get(i)]
        return _round_half_up(sum(ts) / len(ts), 1) if ts else None

    @property
    def cell_stats(self) -> dict | None:
        vs = [v for v in self.cells.values() if v > 0]
        if not vs:
            return None
        return dict(avg=round(sum(vs) / len(vs), 3), min=min(vs), max=max(vs),
                    spread=round(max(vs) - min(vs), 3))

    @property
    def status_flags(self) -> dict[str, bool] | None:
        if self.status is None:
            return None
        v = self.status
        return dict(charge_enable=bool(v & 0x80), discharge_enable=bool(v & 0x40),
                    charge_immediately=bool(v & 0x20), charge_immediately2=bool(v & 0x10),
                    full_charge_request=bool(v & 0x08), small_current_charge_request=bool(v & 0x04))


def apply_registers(pack: Pack, start: int, regs: list[int]) -> None:
    """Apply a response (start register, list of u16) to the pack state."""
    i = 0
    while i < len(regs):
        r, v = start + i, regs[i]
        nxt = regs[i + 1] if i + 1 < len(regs) else None
        if r == 16:
            pack.cell_count = v
        elif 17 <= r <= 36:
            pack.cells[r - 16] = round(v * pack.cell_mult, 3)
        elif r == 37:
            pack.temp_sensor_count = v
        elif 38 <= r <= 47:
            pack.temps_raw[r - 37] = v
        elif r == 48:
            pack.charge_current = round(v * 0.1, 1)
        elif r == 49:
            pack.discharge_current = round(s16(v) * 0.1, 1)
        elif r == 50:
            pack.voltage = round(v * pack.voltage_mult, 3)
        elif r == 51:
            pack.soc = v
        elif r == 52 and nxt is not None:
            pack.capacity_ah = round(((v << 16) | nxt) * 0.001, 3); i += 1
        elif r == 54:
            pack.packs_online = v
        elif r == 55:
            pack.charge_alarm = v
        elif r == 56:
            pack.discharge_alarm = v
        elif r == 57:
            pack.charge_protect = v
        elif r == 58:
            pack.charge_protect2 = v
        elif r == 59:
            pack.discharge_protect = v
        elif r == 60:
            pack.discharge_protect2 = v
        elif r == 62 and nxt is not None:
            pack.design_capacity_ah = round(((v << 16) | nxt) * 0.001, 3); i += 1
        elif r == 112:
            pack.charge_voltage_limit = round(v * 0.1, 1)
        elif r == 113:
            pack.discharge_voltage_limit = round(v * 0.1, 1)
        elif r == 114:
            pack.max_charge_current = round(v * 0.1, 1)
        elif r == 115:
            pack.max_discharge_current = round(v * 0.1, 1)
        elif r == 116:
            pack.status = v
        elif r == 117:
            pack.run_time_to_empty_min = v
        elif r == 118 and nxt is not None:
            pack.remaining_capacity_ah = round(((v << 16) | nxt) * 0.001, 3); i += 1
        i += 1

# ---------------------------------------------------------------- capture parsing


def frames_from_log(path: str):
    """Yield (timestamp, 'TX'|'RX', bytes) from a capture log."""
    for line in open(path):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        ts, direction, *hexbytes = line.split()
        yield ts, direction, bytes.fromhex("".join(hexbytes))


def decode_capture(path: str) -> dict[int, Pack]:
    packs: dict[int, Pack] = {}
    pending = None
    for ts, d, b in frames_from_log(path):
        if d == "TX":
            pending = (b[0], int.from_bytes(b[2:4], "big"), int.from_bytes(b[4:6], "big"))
            continue
        try:
            addr, fc, regs = parse_response(b)
        except FrameError as e:
            print(f"{ts} RX {b.hex(' ')}  -> {e}")
            continue
        if not pending or pending[0] != addr:
            print(f"{ts} RX without matching TX: {b.hex(' ')}")
            continue
        start = pending[1]
        packs.setdefault(addr, Pack(addr))
        apply_registers(packs[addr], start, regs)
        print(f"{ts} addr={addr} start={start:3d} n={len(regs):2d} regs={regs}")
    return packs


def print_packs(packs: dict[int, Pack]) -> None:
    print()
    for a in sorted(packs):
        p = packs[a]
        print(f"=== Pack address {a}")
        print(f"  U={p.voltage} V  I={p.current} A  (charge {p.charge_current} A, discharge {p.discharge_current} A)"
              f"  P={p.power} W  SOC={p.soc} %  capacity={p.capacity_ah} Ah")
        print(f"  T={p.temperature} °C  sensors={p.temp_sensor_count}: "
              + ", ".join(f"T{i}={p.temp_c(i)}" for i in sorted(p.temps_raw) if p.temps_raw[i]))
        if p.cells:
            print(f"  cells={p.cell_count} {p.cell_stats}")
            print("  " + " ".join(f"{p.cells[i]:.3f}" for i in sorted(p.cells) if p.cells[i] > 0))
        if p.max_charge_current is not None:
            print(f"  limits: Uchg={p.charge_voltage_limit} V  Udis={p.discharge_voltage_limit} V  "
                  f"Ichg={p.max_charge_current} A  Idis={p.max_discharge_current} A  status=0x{p.status:02x} {p.status_flags}")

# ---------------------------------------------------------------- self-test on captured frames


def selftest() -> None:
    assert build_request(3, 37, 17).hex() == "03030025001195ef"
    assert build_request(1, 16, 21).hex() == "01030010001585c0"
    assert build_request(2, 112, 5).hex() == "0203007000058421"
    assert expected_response_length(17) == 40

    # Felicity response: 16-bit register count (frame from the capture, address 1, 13:03:34 UTC)
    rx = bytes.fromhex("01 03 00 11 00 04 0b c4 0b c4 0b c4 0b ba 00 00 00 00 00 00 00 00 00 00 00 00"
                       " 00 00 00 13 d0 a2 00 5f 00 05 57 30".replace(" ", ""))
    rx += crc16(rx).to_bytes(2, "little")
    addr, fc, regs = parse_response(rx)
    assert (addr, fc, len(regs)) == (1, 3, 17)
    p = Pack(1)
    apply_registers(p, 37, regs)
    assert p.voltage == 53.41 and p.soc == 95 and p.capacity_ah == 350.0
    assert p.current == -1.9 and p.power == -101
    assert p.temp_c(1) == 28.1 and p.temp_c(4) == 27.1 and p.temperature == 28.1

    # Standard Modbus byte-count header must also pass
    std = bytes([1, 3, 4, 0x0B, 0xC4, 0x00, 0x5F])
    std += crc16(std).to_bytes(2, "little")
    assert parse_response(std)[2] == [3012, 95]

    # Limits and status bits
    p2 = Pack(2)
    apply_registers(p2, 112, [576, 480, 320, 1600, 192])
    assert (p2.charge_voltage_limit, p2.discharge_voltage_limit) == (57.6, 48.0)
    assert (p2.max_charge_current, p2.max_discharge_current) == (32.0, 160.0)
    assert p2.status_flags["charge_enable"] and p2.status_flags["discharge_enable"]
    assert not p2.status_flags["charge_immediately"]

    # Truncated frame is rejected
    try:
        parse_response(rx[:-3] + rx[-2:])
    except FrameError:
        pass
    else:
        raise AssertionError("truncated frame accepted")
    print("selftest OK")


if __name__ == "__main__":
    if len(sys.argv) >= 2 and sys.argv[1] == "--selftest":
        selftest()
    elif len(sys.argv) == 5 and sys.argv[1] == "--request":
        print(build_request(int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])).hex(" "))
    elif len(sys.argv) == 2:
        print_packs(decode_capture(sys.argv[1]))
    else:
        print(__doc__)

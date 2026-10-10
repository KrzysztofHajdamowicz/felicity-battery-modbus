# Felicity Solar battery RS485 protocol (Voltronic "LIB" BMS protocol)

Applies to Felicity Solar FLB48314TG1 (16S LiFePO4, 48 V, 314 Ah) and, by the vendor's own
compatibility notes, to the other Felicity 48 V packs that offer the "Voltronic" RS485 setting
(FLB48100, LPBF48 / LPBA48 family). Verified on three FLB48314TG1 packs in parallel, DIP
addresses 1, 2 and 3, by capturing live bus traffic and cross‑checking every decoded value
against an independent reading of the same packs. Last updated 2026‑10‑10 (version and alarm blocks confirmed with felicityd on the same day).

The packs implement the Voltronic Power document **"Inverter and BMS 485 communication
Protocol"** (Ellis / Lory / Jenny, 2020‑03‑25, file dated 2020‑12‑02), a copy of which is in
`docs/`. Everything below that is marked *verified* was observed on the wire; everything marked
*document* comes from that document and has not yet been confirmed on Felicity firmware.

## 1. Physical layer and framing

| Parameter | Value |
|---|---|
| Interface | RS485, pins 5 (B) and 6 (A) of the battery's RJ45 communication port. CAN is on pins 7–8. |
| Line settings | **9600 baud, 8 data bits, no parity, 1 stop bit**, no flow control |
| Protocol | Modbus RTU framing, function 0x03 only, with a non‑standard response header (§3) |
| CRC | CRC‑16/Modbus (poly 0xA001, init 0xFFFF), low byte first |
| Addresses | 1, 2, 3, … as set by the DIP switches; address 0 may also answer (document: common ID) |
| Response time | 60–80 ms after the request. A 500 ms timeout is plenty. |
| Inter‑frame gap | Document: > 100 ms between frames. One request per second per pack is comfortable. |

Only one master may be present on the bus.

## 2. Request (master → battery)

```
ID | 0x03 | start_hi | start_lo | count_hi | count_lo | CRC_lo | CRC_hi
```

Standard Modbus "Read Holding Registers". All registers of interest are below 256, so
`start_hi` and `count_hi` are always 0x00. Examples:

```
01 03 00 25 00 11 94 0D   address 1, registers 37..53  (17)
01 03 00 10 00 15 85 C0   address 1, registers 16..36  (21)
02 03 00 70 00 05 84 21   address 2, registers 112..116 (5)
```

The largest request confirmed to work is 21 registers. Larger reads (e.g. 16..53 in one frame)
are untested. The document's own limit is not stated.

## 3. Response (battery → master)

```
ID | 0x03 | count_hi | count_lo | reg0_hi | reg0_lo | ... | CRC_lo | CRC_hi
```

**The third and fourth bytes are a 16‑bit register count, not an 8‑bit byte count.** This is
exactly what the Voltronic document specifies ("Data Length, 2 bytes, MSB LSB"), but it is not
Modbus, and standard client libraries will treat such a frame as malformed. Expected frame length
is `4 + 2·count + 2` bytes. Example (address 1, 17 registers from 37):

```
01 03 00 11  00 04 0B C4 0B C4 0B C4 0B BA 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 13 D0 A2 00 5F 00 05 57 30  12 xx
```

A robust parser should accept three header variants, in this order:

1. one byte equal to the number of data bytes (standard Modbus),
2. one byte equal to the number of data bytes + 2,
3. two bytes big‑endian equal to the number of registers (**Felicity**).

Error response per the document: `ID | 0x83 | code | CRC` with code 1 = illegal ID, 2 = illegal
function, 3 = CRC error. Not observed on Felicity.

On a 9600 baud bus with a USB adapter, roughly 1 in 10 responses arrived one byte short
(10 of 111 in `captures/2026-10-10-rs485-bus.log`). Two patterns, both exactly one byte short:

- 3 of 10: the **first byte (slave address) is missing**, so the frame starts with the function
  code `03 00 11 …`. Prepending the address of the outstanding request makes the CRC verify, so
  these frames can be repaired losslessly. This is the classic symptom of a USB‑RS485 adapter
  whose TX→RX turnaround swallows the first byte, i.e. the loss is at the receiver, not the BMS.
- 7 of 10: the **last byte (CRC high byte) is missing**; the data and the CRC low byte are intact.
  Such a frame can only be checked against 8 bits of CRC (1 in 256 false accepts), so a cautious
  implementation discards it and asks again in the next cycle. `felicityd` makes this repair
  opt‑in (`FELICITYD_ACCEPT_TRUNCATED_CRC`).

The simplest robust strategy remains: validate the CRC, discard what does not verify, and ask
again next cycle.

All registers are 16‑bit big‑endian. 32‑bit values occupy two consecutive registers, high word
first.

## 4. Register map

Scaling for Felicity FLB48314TG1 ("HD" variant: mV everywhere). The Voltronic document specifies
0.1 V for cell and pack voltage; older/other firmware may use that, see §6.

| Reg (dec) | Hex | Name | Type | Scale | Unit | Status | Observed / notes |
|---|---|---|---|---|---|---|---|
| 1 | 0x01 | Protocol type | u16 | 1 | – | verified | reads 0 |
| 2 | 0x02 | Protocol version | u16 | 1 | – | verified | reads 0 |
| 3–4 | 0x03 | BMS firmware version | u32 | – | – | verified | 0x0001_0001 → "1.1" (high word.low word) on all three packs |
| 5–6 | 0x05 | BMS hardware version | u32 | – | – | verified | 0x0001_0001 → "1.1" |
| 16 | 0x10 | Number of cells | u16 | 1 | – | verified | 16 |
| 17–36 | 0x11–0x24 | Cell voltage 1..20 | u16 | 0.001 | V | verified | 3338–3344; cells beyond "Number of cells" read 0 |
| 37 | 0x25 | Number of temperature sensors | u16 | 1 | – | verified | 4 |
| 38–47 | 0x26–0x2F | Temperature 1..10 | u16 | 0.1 K | – | verified | 3002, 3012 → 27.05 / 28.05 °C; sensor 6 is the MOSFET per document; unused sensors read 0 |
| 48 | 0x30 | Charge current | u16 | 0.1 | A | verified | 0 when discharging |
| 49 | 0x31 | Discharge current | s16 | 0.1 | A | verified | 0 when charging; use the absolute value |
| 50 | 0x32 | Pack voltage | u16 | 0.001 | V | verified | 53400–53470 |
| 51 | 0x33 | State of charge | u16 | 1 | % | verified | 95, 96 |
| 52–53 | 0x34 | Full‑charge capacity | u32 | 0.001 | Ah | verified | 0x0005_5730 = 350000 mAh = 350.0 Ah |
| 54 | 0x36 | Packs online | u16 | 1 | – | answered | block 54..60 is answered by Felicity firmware 1.1; all registers read 0 on a healthy system, so the bit meanings are still unconfirmed |
| 55 | 0x37 | Charge alarm | bits | – | – | answered (0) | bit0 CHT, bit1 COV, bit2 CLT, bit3 OCC |
| 56 | 0x38 | Discharge alarm | bits | – | – | answered (0) | bit0 DHT, bit1 DLT, bit2 FETHT, bit3 CUV |
| 57 | 0x39 | Charge protect | bits | – | – | answered (0) | see §7 |
| 58 | 0x3A | Charge protect 2 | bits | – | – | answered (0) | see §7 |
| 59 | 0x3B | Discharge protect | bits | – | – | answered (0) | see §7 |
| 60 | 0x3C | Discharge protect 2 | bits | – | – | answered (0) | see §7 |
| 61 | 0x3D | BMS state | u16 | – | – | document | reserved, 0 |
| 62–63 | 0x3E | Design capacity | u32 | 0.001 | Ah | document | |
| 64–74 | 0x40–0x4A | Cell count + cell‑pair voltage state | u16 | – | – | document | 00 normal, 01 below limit, 02 above limit |
| 80–85 | 0x50–0x55 | NTC count + sensor‑pair temperature state | u16 | – | – | document | same coding |
| 96–105 | 0x60–0x69 | Module / cell voltage, current and temperature states | u16 | – | – | document | same coding |
| 112 | 0x70 | Charge voltage limit | u16 | 0.1 | V | verified | 576 → 57.6 V |
| 113 | 0x71 | Discharge voltage limit | u16 | 0.1 | V | verified | 480 → 48.0 V |
| 114 | 0x72 | Charge current limit | u16 | 0.1 | A | verified | 320 / 400 / 480 → 32–48 A, differs per pack |
| 115 | 0x73 | Discharge current limit | u16 | 0.1 | A | verified | 1600 → 160 A |
| 116 | 0x74 | Charge / discharge status | bits | – | – | verified | 0xC0 = charge enable + discharge enable, see §7 |
| 117 | 0x75 | Run time to empty | u16 | 1 | min | document | |
| 118–119 | 0x76 | Remaining capacity | u32 | 0.001 | Ah | document | |

Registers 7–15, 75–79, 86–95 and 106–111 are not described by the document. The document says
unimplemented parameters read as 0x0000.

## 5. Derived values

```
temperature_n [°C] = raw_n / 10 − 273.15            (raw > 1000; else treat raw as whole kelvin)
current       [A]  = charge_current − |discharge_current|      (negative = discharging)
power         [W]  = current × pack_voltage
pack temperature   = mean of sensors 1..3 (sensor 4 on FLB48314TG1 reads ~1 K lower, likely a different location)
cell average / min / max / spread = over cells 1..number_of_cells with value > 0
```

Worked example (address 1, registers 37..53 = `4, 3012, 3012, 3012, 3002, 0×6, 0, 19, 53410, 95, 5, 22320`):
53.41 V, −1.9 A, −101 W, SOC 95 %, 350.0 Ah, 28.1 °C.

## 6. Pack discovery and scaling detection

Recommended start‑up sequence (works with the observed firmware):

1. Read register 51 (SOC) from address 0, then 1, 2, … until the first pack answers. Keep going
   while consecutive addresses answer; stop at the first address with no response, or at 16.
   Wait ~500 ms between probes. A response with a zero register count means "blank pack".
2. Read registers 16..20. If cell 1 > 1000, cells are in mV. Then read register 50: if it is
   ≥ 10000 the pack voltage is in mV too (FLB48314TG1), otherwise in 0.1 V. If cell 1 ≤ 1000,
   both are in 0.1 V as in the document. If register 50 reads 0, the pack only supports the
   48..53 block.

## 7. Bit fields (from the Voltronic document)

**0x74 Charge / discharge status** (verified value 0xC0): bit7 charge enable, bit6 discharge
enable, bit5 charge immediately (SOC ≤ 9 %), bit4 charge immediately 2 (SOC 10–14 %), bit3 full
charge request, bit2 small‑current charge request.

**0x37 Charge alarm:** bit0 CHT charge over‑temperature, bit1 COV cell over‑voltage, bit2 CLT
charge low‑temperature, bit3 OCC charge over‑current.

**0x38 Discharge alarm:** bit0 DHT discharge over‑temperature, bit1 DLT discharge
low‑temperature, bit2 FETHT MOSFET over‑temperature, bit3 CUV cell under‑voltage.

**0x39 Charge protect:** bit0 PRES ("in system" signal), bit1 BoostNRDY MOSFET driver, bit2
AFEComm AFE communication fail, bit3 AFEOCC, bit4 AFEOCD, bit5 AFESCD, bit6 FETHT, bit7 SUV
safety under‑voltage, bit8 CHT, bit9 CLT, bit10 OCV, bit11 OCC, bit12 2NDOVP, bit13 CANID,
bit15 OCC2.

**0x3A Charge protect 2:** bit0 ShutdownByCmd, bit1 Shutdown (low voltage), bit2 OCCHw charge
short circuit, bit3 IDError CAN ID error.

**0x3B Discharge protect:** bit0 PRES, bit1 BoostNRDY, bit2 AFEOCC, bit3 AFEOCD, bit4 AFESCD,
bit6 IDError, bit10 FETHT, bit11 CUV, bit12 OCD, bit13 DLT, bit14 DHT, bit15 Shutdown.

**0x3C Discharge protect 2:** bit0 AFEComm, bit1 Short, bit2 OCD2, bit4 2NDOVP, bit5 DHT2,
bit6 ShutdownByCmd, bit13 CANID.

## 8. Suggested polling plan

| Block | Registers | Interval | Why |
|---|---|---|---|
| Status | 37..53 (17) | every 2–5 s per pack | temperatures, current, voltage, SOC, capacity |
| Cells | 16..36 (21) | every 20–30 s per pack | cell voltages change slowly |
| Limits + status bits | 112..116 (5) | every 1–5 min per pack | limits change with temperature/SOC |
| Alarms | 54..63 (10) | every 10–30 s per pack | *untested on Felicity*, read once and check for non‑zero |

With three packs and one request per ~1.3 s, a full status refresh for every pack takes ~4 s.

## 9. Open questions

- Felicity firmware 1.1 answers the version block (0x01–0x06) and the alarm / protection block
  (0x36–0x3C); the latter read 0x0000 on a healthy system, so the bit assignments of §7 remain
  unconfirmed until an alarm is actually observed. State registers (0x40–0x69) and remaining
  capacity (0x75–0x77) are still untested.
- Maximum register count per request.
- Whether address 0 (document: "common ID, every BMS answers") is honoured by Felicity. With DIP
  addresses 1–3 each address returns its own pack's data.
- Writes. The document lists function 0x10 but no writable registers.

## 10. References

- Voltronic Power, *Inverter and BMS 485 communication Protocol*, 2020‑03‑25, `docs/`.
- Felicity Solar inverter connection guides (RS485 pin assignment, DIP addressing).

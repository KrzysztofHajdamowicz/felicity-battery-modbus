# felicity-battery-modbus

RS485 / Modbus RTU protocol documentation and tooling for **Felicity Solar LiFePO4 batteries**
(FLB48314TG1, and other 48 V packs that speak the **Voltronic "LIB" BMS protocol**: FLB48100,
LPBF48, LPBA48 family), with the goal of reading the packs directly from
**Home Assistant** (MQTT discovery) and, later, from an **ESPHome** device.

> Status: protocol decoded and verified on hardware (3 packs in parallel). Daemon and ESPHome
> component are planned, see *Roadmap*.

## What is in here

| Path | Contents |
|---|---|
| [`docs/protocol.md`](docs/protocol.md) | Full protocol description: wiring, framing, the non‑standard response header, register map, scaling, derived metrics, pack discovery, polling plan. |
| [`docs/felicity-battery-registers.pdf`](docs/felicity-battery-registers.pdf) | The register map as a printable PDF. |
| [`docs/voltronic-inverter-bms-485-protocol-20201202.docx`](docs/voltronic-inverter-bms-485-protocol-20201202.docx) | The original Voltronic Power document *"Inverter and BMS 485 communication Protocol"* (2020‑03‑25) that the batteries implement, plus a plain‑text extract. |
| [`tools/felicity_decoder.py`](tools/felicity_decoder.py) | Reference decoder in Python: request builder, response validator (handles the non‑standard header), register map, derived metrics, bus‑capture parser, self‑test. |
| [`captures/`](captures/) | Raw RS485 traffic from a live system (3 × FLB48314TG1 at addresses 1–3). |

## Protocol in one paragraph

Modbus RTU, **9600 8N1**, function **0x03** only. Requests are standard 8‑byte frames. **Responses
are not standard:** after the address and function code the battery sends a **16‑bit register
count** instead of the 8‑bit byte count, so off‑the‑shelf Modbus stacks (pymodbus, tmodbus,
ESPHome `modbus_controller`) reject every frame as a length/CRC error. The register layout follows
the Voltronic document: cell count and 20 cell voltages from register 16, temperature count and
10 NTC readings (0.1 K) from 37, charge/discharge current (0.1 A), pack voltage, SOC and capacity
(mAh) at 48–53, charge/discharge limits and status bits at 112–116. Felicity deviates from the
document by reporting cell and pack voltage in **mV** rather than 0.1 V.

Example exchange (address 1, registers 37..53):

```
TX  01 03 00 25 00 11 94 0D
RX  01 03 00 11  00 04  0B C4 0B C4 0B C4 0B BA  00 00 00 00 00 00 00 00 00 00 00 00  00 00  00 13  D0 A2  00 5F  00 05 57 30  <CRC>
          ^^^^^ 17 registers (not a byte count)
```

→ 4 sensors, 28.1 / 28.1 / 28.1 / 27.1 °C, charge 0.0 A, discharge 1.9 A, 53.41 V, SOC 95 %, 350.0 Ah.

## Hardware notes

- Battery communication port: RS485 on pins 5 (B) and 6 (A) of the RJ45 socket, CAN on 7–8
  (see the vendor's inverter connection guides). A plain USB‑RS485 dongle works.
- Pack address is set with the DIP switches on each pack (1, 2, 3, …). The master polls each
  address separately; the packs do not aggregate.
- Only **one master** may be on the bus. If an inverter already reads the packs over RS485, use
  the inverter's data instead, or move the inverter to CAN.

## Roadmap

1. `felicityd`: a small Go daemon for a Linux box with a USB‑RS485 adapter (Raspberry Pi), which
   discovers the packs, polls them, and publishes per‑pack sensors to MQTT with Home Assistant
   discovery (one HA device per pack plus a system total).
2. ESPHome external component (C++) for an ESP32 with an RS485 transceiver, generating the
   sensors from a `packs:` option instead of hand‑written YAML.
3. Verify the alarm / protection registers (0x36–0x3C) and the remaining‑capacity registers
   (0x75–0x77) from the Voltronic document on Felicity firmware.

## Quick check with the reference decoder

```bash
python3 tools/felicity_decoder.py --selftest
python3 tools/felicity_decoder.py captures/2026-10-10-rs485-bus.log
python3 tools/felicity_decoder.py --request 1 37 17     # prints the request frame in hex
```

## License

MIT, see [LICENSE](LICENSE). The Voltronic document in `docs/` is the property of Voltronic Power
and is included for reference.

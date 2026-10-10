package modbus

// Truncated frames are common on a USB-RS485 adapter (about 1 in 10 in captures/, see
// docs/protocol.md §3). Two patterns were observed, both exactly one byte short:
//
//   - the first byte (slave address) is missing, typically because the adapter's TX→RX
//     turnaround swallows it. The frame then starts with the function code 0x03;
//   - the last byte (CRC high byte) is missing.
//
// RecoverFrame repairs the first pattern with a full 16-bit CRC check, which is as safe as a
// normal frame. The second pattern can only be verified against the CRC low byte (8 bits), so it
// is opt-in.

// RecoverOptions controls which repairs RecoverFrame attempts.
type RecoverOptions struct {
	// ExpectedAddress is the slave address of the outstanding request (0 = unknown, no repair).
	ExpectedAddress byte
	// ExpectedLength is the well-formed frame length for the outstanding request.
	ExpectedLength int
	// AcceptTruncatedCRC accepts frames whose final CRC byte is missing when the CRC low byte
	// matches. One chance in 256 of accepting a corrupted frame.
	AcceptTruncatedCRC bool
}

// RecoverFrame returns a repaired copy of frame and true when a repair verified against the CRC,
// or the original frame and false otherwise. It only touches frames that are exactly one byte
// shorter than ExpectedLength.
func RecoverFrame(frame []byte, o RecoverOptions) ([]byte, bool) {
	if o.ExpectedLength == 0 || len(frame) != o.ExpectedLength-1 || len(frame) < 4 {
		return frame, false
	}
	// Missing address byte: the frame starts with the function code.
	if o.ExpectedAddress != 0 && frame[0] == FuncReadHolding {
		fixed := make([]byte, 0, len(frame)+1)
		fixed = append(fixed, o.ExpectedAddress)
		fixed = append(fixed, frame...)
		if crcOK(fixed) {
			return fixed, true
		}
	}
	// Missing CRC high byte: verify the low byte, then re-append the computed CRC.
	if o.AcceptTruncatedCRC && (o.ExpectedAddress == 0 || frame[0] == o.ExpectedAddress) {
		crc := CRC16(frame[:len(frame)-1])
		if byte(crc) == frame[len(frame)-1] {
			fixed := make([]byte, 0, len(frame)+1)
			fixed = append(fixed, frame[:len(frame)-1]...)
			fixed = append(fixed, byte(crc), byte(crc>>8))
			return fixed, true
		}
	}
	return frame, false
}

func crcOK(frame []byte) bool {
	n := len(frame)
	return CRC16(frame[:n-2]) == uint16(frame[n-2])|uint16(frame[n-1])<<8
}

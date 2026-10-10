package modbus

import (
	"os"
	"testing"
)

func TestRecoverFrame(t *testing.T) {
	full := withCRC(mustHex(t, "01 03 00 11 00 04 0b c4 0b c4 0b c4 0b ba 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 13 d0 a2 00 5f 00 05 57 30"))
	exp := ExpectedResponseLength(17)

	// Missing address byte: repaired with a full CRC check.
	noAddr := full[1:]
	fixed, ok := RecoverFrame(noAddr, RecoverOptions{ExpectedAddress: 1, ExpectedLength: exp})
	if !ok || string(fixed) != string(full) {
		t.Errorf("missing address not recovered: ok=%v", ok)
	}
	// Wrong expected address must not verify.
	if _, ok := RecoverFrame(noAddr, RecoverOptions{ExpectedAddress: 2, ExpectedLength: exp}); ok {
		t.Error("recovered with wrong address")
	}
	// Unknown address: no repair.
	if _, ok := RecoverFrame(noAddr, RecoverOptions{ExpectedLength: exp}); ok {
		t.Error("recovered without expected address")
	}

	// Missing CRC high byte: only with AcceptTruncatedCRC.
	noCRC := full[:len(full)-1]
	if _, ok := RecoverFrame(noCRC, RecoverOptions{ExpectedAddress: 1, ExpectedLength: exp}); ok {
		t.Error("truncated CRC accepted without opt-in")
	}
	fixed, ok = RecoverFrame(noCRC, RecoverOptions{ExpectedAddress: 1, ExpectedLength: exp, AcceptTruncatedCRC: true})
	if !ok || string(fixed) != string(full) {
		t.Errorf("truncated CRC not recovered: ok=%v", ok)
	}
	// Corrupted data with truncated CRC must fail the low-byte check.
	bad := append([]byte{}, noCRC...)
	bad[10] ^= 0x01
	if _, ok := RecoverFrame(bad, RecoverOptions{ExpectedAddress: 1, ExpectedLength: exp, AcceptTruncatedCRC: true}); ok {
		t.Error("corrupted frame accepted")
	}
	// Wrong length: untouched.
	if _, ok := RecoverFrame(full, RecoverOptions{ExpectedAddress: 1, ExpectedLength: exp, AcceptTruncatedCRC: true}); ok {
		t.Error("complete frame 'recovered'")
	}
}

// Documents the truncation pattern in the capture: 10 frames one byte short, 3 missing the
// address byte (fully recoverable) and 7 missing the last CRC byte (recoverable with the 8-bit
// check only).
func TestCaptureRecovery(t *testing.T) {
	f, err := os.Open("../../captures/2026-10-10-rs485-bus.log")
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	frames, err := ReadCapture(f)
	if err != nil {
		t.Fatal(err)
	}
	var rejected, viaAddress, viaCRC int
	var addr byte
	var expLen int
	for _, fr := range frames {
		if fr.TX {
			addr = fr.Data[0]
			expLen = ExpectedResponseLength(int(fr.Data[4])<<8 | int(fr.Data[5]))
			continue
		}
		if _, err := ParseResponse(fr.Data); err == nil {
			continue
		}
		rejected++
		if fixed, ok := RecoverFrame(fr.Data, RecoverOptions{ExpectedAddress: addr, ExpectedLength: expLen}); ok {
			viaAddress++
			if r, err := ParseResponse(fixed); err != nil || r.Address != addr || len(r.Registers) != (expLen-6)/2 {
				t.Errorf("%s: repaired frame does not parse: %v", fr.Timestamp, err)
			}
			continue
		}
		if fixed, ok := RecoverFrame(fr.Data, RecoverOptions{ExpectedAddress: addr, ExpectedLength: expLen, AcceptTruncatedCRC: true}); ok {
			viaCRC++
			if _, err := ParseResponse(fixed); err != nil {
				t.Errorf("%s: repaired frame does not parse: %v", fr.Timestamp, err)
			}
		}
	}
	if rejected != 10 || viaAddress != 3 || viaCRC != 7 {
		t.Errorf("rejected=%d recovered via address=%d via crc low byte=%d; want 10/3/7", rejected, viaAddress, viaCRC)
	}
}

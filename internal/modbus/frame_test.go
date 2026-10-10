package modbus

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func withCRC(b []byte) []byte {
	crc := CRC16(b)
	return append(append([]byte{}, b...), byte(crc), byte(crc>>8))
}

func TestBuildRequest(t *testing.T) {
	cases := []struct {
		addr         byte
		start, count uint16
		want         string
	}{
		{3, 37, 17, "03030025001195ef"},
		{1, 16, 21, "01030010001585c0"},
		{2, 112, 5, "0203007000058421"},
	}
	for _, c := range cases {
		if got := hex.EncodeToString(BuildRequest(c.addr, c.start, c.count)); got != c.want {
			t.Errorf("BuildRequest(%d,%d,%d) = %s, want %s", c.addr, c.start, c.count, got, c.want)
		}
	}
	if ExpectedResponseLength(17) != 40 {
		t.Errorf("ExpectedResponseLength(17) = %d", ExpectedResponseLength(17))
	}
}

func TestParseResponseFelicityHeader(t *testing.T) {
	rx := withCRC(mustHex(t, "01 03 00 11 00 04 0b c4 0b c4 0b c4 0b ba 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 13 d0 a2 00 5f 00 05 57 30"))
	resp, err := ParseResponse(rx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Address != 1 || resp.Function != 3 || len(resp.Registers) != 17 {
		t.Fatalf("got %+v", resp)
	}
	if resp.Registers[0] != 4 || resp.Registers[13] != 53410 || resp.Registers[14] != 95 {
		t.Errorf("registers = %v", resp.Registers)
	}
	if U32(resp.Registers[15], resp.Registers[16]) != 350000 {
		t.Errorf("capacity = %d", U32(resp.Registers[15], resp.Registers[16]))
	}

	// Truncated by one byte must be rejected.
	trunc := withCRC(rx[:len(rx)-3])
	if _, err := ParseResponse(trunc); !errors.Is(err, ErrPayload) {
		t.Errorf("truncated frame: err = %v", err)
	}
	// Corrupted CRC.
	bad := append([]byte{}, rx...)
	bad[5] ^= 0xff
	if _, err := ParseResponse(bad); !errors.Is(err, ErrCRC) {
		t.Errorf("corrupted frame: err = %v", err)
	}
}

func TestParseResponseStandardHeaders(t *testing.T) {
	std := withCRC([]byte{1, 3, 4, 0x0B, 0xC4, 0x00, 0x5F})
	resp, err := ParseResponse(std)
	if err != nil || len(resp.Registers) != 2 || resp.Registers[0] != 3012 || resp.Registers[1] != 95 {
		t.Fatalf("standard header: %v %v", resp, err)
	}
	plus2 := withCRC([]byte{1, 3, 6, 0x0B, 0xC4, 0x00, 0x5F})
	if resp, err := ParseResponse(plus2); err != nil || len(resp.Registers) != 2 {
		t.Fatalf("byte count + 2 header: %v %v", resp, err)
	}
}

func TestParseResponseErrors(t *testing.T) {
	if _, err := ParseResponse(nil); !errors.Is(err, ErrNoResponse) {
		t.Errorf("empty: %v", err)
	}
	if _, err := ParseResponse([]byte{1, 3}); !errors.Is(err, ErrShort) {
		t.Errorf("short: %v", err)
	}
	var ex *ExceptionError
	if _, err := ParseResponse(withCRC([]byte{1, 0x83, 2})); !errors.As(err, &ex) || ex.Code != 2 {
		t.Errorf("exception: %v", err)
	}
	if _, err := ParseResponse(withCRC([]byte{1, 3, 0})); !errors.Is(err, ErrBlank) {
		t.Errorf("blank: %v", err)
	}
	if S16(0xFFFF) != -1 || S16(0x0013) != 19 {
		t.Error("S16")
	}
}

// Every RX frame in the bus capture must either decode to the register count announced by the
// preceding TX, or be rejected (truncated frames are expected on the wire, see protocol.md §3).
func TestCapture(t *testing.T) {
	f, err := os.Open("../../captures/2026-10-10-rs485-bus.log")
	if err != nil {
		t.Skip("capture not available:", err)
	}
	defer f.Close()
	frames, err := ReadCapture(f)
	if err != nil {
		t.Fatal(err)
	}
	var tx, ok, rejected int
	var pendingCount int
	var pendingAddr byte
	for _, fr := range frames {
		if fr.TX {
			tx++
			pendingAddr = fr.Data[0]
			pendingCount = int(fr.Data[4])<<8 | int(fr.Data[5])
			if CRC16(fr.Data[:6]) != uint16(fr.Data[6])|uint16(fr.Data[7])<<8 {
				t.Errorf("%s TX with bad CRC", fr.Timestamp)
			}
			continue
		}
		resp, err := ParseResponse(fr.Data)
		if err != nil {
			rejected++
			continue
		}
		ok++
		if resp.Address != pendingAddr {
			t.Errorf("%s RX from %d, expected %d", fr.Timestamp, resp.Address, pendingAddr)
		}
		if len(resp.Registers) != pendingCount {
			t.Errorf("%s RX %d registers, expected %d", fr.Timestamp, len(resp.Registers), pendingCount)
		}
	}
	if tx == 0 || ok == 0 {
		t.Fatalf("capture had %d TX and %d decodable RX frames", tx, ok)
	}
	t.Logf("capture: %d TX, %d RX ok, %d RX rejected", tx, ok, rejected)
}

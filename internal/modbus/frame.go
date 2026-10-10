// Package modbus implements the Modbus RTU framing used by Felicity Solar / Voltronic "LIB"
// batteries: standard read-holding-registers requests and the non-standard response header
// (a 16-bit register count instead of the 8-bit byte count). See docs/protocol.md §2–§3.
package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// FuncReadHolding is the only function code the batteries implement.
const FuncReadHolding = 0x03

// ErrNoResponse, ErrCRC and friends are the classification used by callers to decide between
// "ask again next cycle" and "feature unsupported".
var (
	ErrNoResponse = errors.New("no response")
	ErrShort      = errors.New("frame too short")
	ErrCRC        = errors.New("crc mismatch")
	ErrBlank      = errors.New("blank pack (empty payload)")
	ErrPayload    = errors.New("payload length does not match header")
)

// ExceptionError is a Modbus exception response (function | 0x80).
type ExceptionError struct {
	Address  byte
	Function byte
	Code     byte
}

func (e *ExceptionError) Error() string {
	return fmt.Sprintf("modbus exception from %d: function 0x%02x code %d", e.Address, e.Function&0x7f, e.Code)
}

// CRC16 computes CRC-16/Modbus (poly 0xA001, init 0xFFFF).
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// BuildRequest returns the 8-byte read-holding-registers request for the given slave address.
func BuildRequest(address byte, start, count uint16) []byte {
	pdu := make([]byte, 8)
	pdu[0] = address
	pdu[1] = FuncReadHolding
	binary.BigEndian.PutUint16(pdu[2:], start)
	binary.BigEndian.PutUint16(pdu[4:], count)
	binary.LittleEndian.PutUint16(pdu[6:], CRC16(pdu[:6]))
	return pdu
}

// ExpectedResponseLength is the frame length for the Felicity header variant:
// address, function, 16-bit register count, 2*count data bytes, CRC.
func ExpectedResponseLength(count int) int {
	return 4 + 2*count + 2
}

// Response is a validated read-holding-registers reply.
type Response struct {
	Address   byte
	Function  byte
	Registers []uint16
}

// ParseResponse validates the CRC and decodes the payload, accepting three header variants
// after <address, function> in this order:
//
//  1. one byte equal to the number of data bytes (standard Modbus),
//  2. one byte equal to the number of data bytes + 2,
//  3. two bytes big-endian equal to the number of registers (Felicity / Voltronic document).
func ParseResponse(frame []byte) (Response, error) {
	if len(frame) == 0 {
		return Response{}, ErrNoResponse
	}
	if len(frame) < 3 {
		return Response{}, ErrShort
	}
	if CRC16(frame[:len(frame)-2]) != binary.LittleEndian.Uint16(frame[len(frame)-2:]) {
		return Response{}, ErrCRC
	}
	addr, fc := frame[0], frame[1]
	if fc&0x80 != 0 {
		return Response{}, &ExceptionError{Address: addr, Function: fc, Code: frame[2]}
	}
	body := frame[2 : len(frame)-2]
	if len(body) < 2 {
		return Response{}, ErrBlank
	}
	var data []byte
	n8, rest8 := int(body[0]), body[1:]
	if n8 == len(rest8) || n8 == len(rest8)+2 {
		data = rest8
	} else {
		n16, rest16 := int(binary.BigEndian.Uint16(body[0:2])), body[2:]
		if n16*2 != len(rest16) {
			return Response{}, ErrPayload
		}
		data = rest16
	}
	regs := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		regs = append(regs, binary.BigEndian.Uint16(data[i:]))
	}
	return Response{Address: addr, Function: fc, Registers: regs}, nil
}

// S16 reinterprets a register as a signed 16-bit value.
func S16(v uint16) int16 { return int16(v) }

// U32 joins two registers, high word first.
func U32(hi, lo uint16) uint32 { return uint32(hi)<<16 | uint32(lo) }

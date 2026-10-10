package bus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.bug.st/serial"
)

// SerialTransport talks to the packs over a USB-RS485 adapter: 9600 8N1 (docs/protocol.md §1).
type SerialTransport struct {
	port    serial.Port
	timeout time.Duration // wait for the first byte of the reply
	mu      sync.Mutex
}

// interByteTimeout ends a reply once the line has been silent this long. At 9600 baud a byte takes
// ~1 ms, so 30 ms is comfortably above the Modbus 3.5-character gap yet far below the pack's
// 60–80 ms response time.
const interByteTimeout = 30 * time.Millisecond

// OpenSerial opens the port at 9600 8N1. timeout is the response timeout (500 ms is plenty).
func OpenSerial(path string, timeout time.Duration) (*SerialTransport, error) {
	port, err := serial.Open(path, &serial.Mode{
		BaudRate: 9600,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &SerialTransport{port: port, timeout: timeout}, nil
}

// Exchange writes the request and collects the reply until expectLen bytes have arrived or the
// line goes quiet. It returns an empty slice (no error) on timeout so the caller sees ErrNoResponse
// from the parser.
func (s *SerialTransport) Exchange(ctx context.Context, request []byte, expectLen int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.port.ResetInputBuffer(); err != nil {
		return nil, fmt.Errorf("reset input: %w", err)
	}
	if _, err := s.port.Write(request); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	// Drain is best-effort: on platforms where it is not implemented the write has already been
	// handed to the driver and the read loop below simply waits for the reply.
	_ = s.port.Drain()

	buf := make([]byte, 0, expectLen+16)
	chunk := make([]byte, 64)
	deadline := time.Now().Add(s.timeout)
	for len(buf) < expectLen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wait := interByteTimeout
		if len(buf) == 0 {
			wait = time.Until(deadline)
			if wait <= 0 {
				break
			}
		}
		if err := s.port.SetReadTimeout(wait); err != nil {
			return nil, fmt.Errorf("set read timeout: %w", err)
		}
		n, err := s.port.Read(chunk)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		if n == 0 { // timeout
			break
		}
		buf = append(buf, chunk[:n]...)
	}
	// Absorb any trailing bytes (e.g. a longer-than-expected standard-header frame) so they do
	// not pollute the next exchange.
	if len(buf) >= expectLen {
		_ = s.port.SetReadTimeout(interByteTimeout)
		for {
			n, err := s.port.Read(chunk)
			if err != nil || n == 0 {
				break
			}
			buf = append(buf, chunk[:n]...)
		}
	}
	return buf, nil
}

// Close closes the port.
func (s *SerialTransport) Close() error { return s.port.Close() }

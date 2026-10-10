// Package bus owns the RS485 bus: a Transport abstraction (real serial port or capture replay),
// pack enumeration (docs/protocol.md §6) and the polling scheduler (§8).
package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/modbus"
)

// Transport sends one request and returns whatever bytes arrived in reply (possibly none, possibly
// truncated). expectLen is a hint for the reader: the frame length of a well-formed reply.
type Transport interface {
	Exchange(ctx context.Context, request []byte, expectLen int) ([]byte, error)
	Close() error
}

// ErrAddressMismatch and ErrCountMismatch mean a frame decoded fine but was not the answer to our
// question (e.g. a late reply from another pack).
var (
	ErrAddressMismatch = errors.New("response from unexpected address")
	ErrCountMismatch   = errors.New("response register count differs from request")
)

// AcceptTruncatedCRC enables the weaker (8-bit) repair of frames missing their last CRC byte,
// see modbus.RecoverOptions. Set once at start-up from the configuration.
var AcceptTruncatedCRC bool

// ReadBlock reads one block from a pack and applies it. Transport errors and malformed frames are
// returned as-is so callers can distinguish "no answer" from "garbage".
func ReadBlock(ctx context.Context, t Transport, p *felicity.Pack, b felicity.Block) ([]uint16, error) {
	regs, err := ReadRegisters(ctx, t, p.Address, b.Start, b.Count)
	if err != nil {
		return nil, err
	}
	p.Apply(b.Start, regs)
	p.LastUpdate = time.Now()
	return regs, nil
}

// ReadRegisters performs one request/response cycle and validates the reply. Frames that lost
// their first byte on the wire are repaired when the CRC still verifies (modbus.RecoverFrame).
func ReadRegisters(ctx context.Context, t Transport, addr byte, start, count uint16) ([]uint16, error) {
	req := modbus.BuildRequest(addr, start, count)
	expLen := modbus.ExpectedResponseLength(int(count))
	raw, err := t.Exchange(ctx, req, expLen)
	if err != nil {
		return nil, err
	}
	resp, err := modbus.ParseResponse(raw)
	if err != nil {
		fixed, ok := modbus.RecoverFrame(raw, modbus.RecoverOptions{
			ExpectedAddress: addr, ExpectedLength: expLen, AcceptTruncatedCRC: AcceptTruncatedCRC,
		})
		if !ok {
			return nil, err
		}
		if resp, err = modbus.ParseResponse(fixed); err != nil {
			return nil, err
		}
	}
	if resp.Address != addr {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrAddressMismatch, resp.Address, addr)
	}
	if len(resp.Registers) != int(count) {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrCountMismatch, len(resp.Registers), count)
	}
	return resp.Registers, nil
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func logger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

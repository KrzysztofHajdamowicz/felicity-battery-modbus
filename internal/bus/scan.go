package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
)

// ScanOptions controls pack enumeration.
type ScanOptions struct {
	// Addresses, when non-empty, skips enumeration and probes exactly these packs.
	Addresses []byte
	// MaxAddress is the highest address probed during enumeration (default 16).
	MaxAddress byte
	// Gap is the pause between probes (default 500 ms, docs/protocol.md §6).
	Gap time.Duration
	// Retries per probe (default 2).
	Retries int
	Log     *slog.Logger
}

// ErrNoPacks is returned when enumeration finds nothing.
var ErrNoPacks = errors.New("no battery packs found on the bus")

// Scan enumerates the packs (docs/protocol.md §6): probe register 51 at addresses 1, 2, … until the
// first one that does not answer, detect the voltage scaling, try the optional version and alarm
// blocks, and read the regular blocks once so every returned pack is Complete().
func Scan(ctx context.Context, t Transport, o ScanOptions) ([]*felicity.Pack, error) {
	log := logger(o.Log)
	if o.MaxAddress == 0 {
		o.MaxAddress = 16
	}
	if o.Gap == 0 {
		o.Gap = 500 * time.Millisecond
	}
	if o.Retries == 0 {
		o.Retries = 2
	}

	var packs []*felicity.Pack
	probe := func(addr byte) (bool, error) {
		for i := 0; i < o.Retries; i++ {
			_, err := ReadRegisters(ctx, t, addr, felicity.BlockProbe.Start, felicity.BlockProbe.Count)
			if err == nil {
				return true, nil
			}
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			log.Debug("probe failed", "address", addr, "attempt", i+1, "err", err)
			if err := sleep(ctx, o.Gap); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	if len(o.Addresses) > 0 {
		for _, a := range o.Addresses {
			ok, err := probe(a)
			if err != nil {
				return nil, err
			}
			if !ok {
				log.Warn("configured pack did not answer, will keep polling it", "address", a)
			}
			p := felicity.NewPack(a)
			p.Online = ok
			packs = append(packs, p)
		}
	} else {
		for a := byte(1); a <= o.MaxAddress; a++ {
			ok, err := probe(a)
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			log.Info("pack found", "address", a)
			p := felicity.NewPack(a)
			p.Online = true
			packs = append(packs, p)
			if err := sleep(ctx, o.Gap); err != nil {
				return nil, err
			}
		}
	}
	if len(packs) == 0 {
		return nil, ErrNoPacks
	}

	for _, p := range packs {
		if !p.Online {
			continue
		}
		if err := Initialise(ctx, t, p, o.Gap, log); err != nil {
			return nil, fmt.Errorf("pack %d: %w", p.Address, err)
		}
	}
	return packs, nil
}

// Initialise detects scaling, probes the optional blocks and reads the regular blocks once.
// Only a cancelled context is an error; a pack that stops answering is left incomplete and offline.
func Initialise(ctx context.Context, t Transport, p *felicity.Pack, gap time.Duration, log *slog.Logger) error {
	log = logger(log)
	read := func(b felicity.Block, attempts int) bool {
		for i := 0; i < attempts; i++ {
			_, err := ReadBlock(ctx, t, p, b)
			if err == nil {
				_ = sleep(ctx, gap)
				return true
			}
			log.Debug("block read failed", "address", p.Address, "block", b.Name, "attempt", i+1, "err", err)
			if ctx.Err() != nil {
				return false
			}
			_ = sleep(ctx, gap)
		}
		return false
	}

	// Scaling detection (§6 step 2): cells 16..20, then pack voltage 50.
	cell1, packV := uint16(0), uint16(0)
	if regs, err := ReadRegisters(ctx, t, p.Address, felicity.BlockScale.Start, felicity.BlockScale.Count); err == nil {
		cell1 = regs[1]
	}
	_ = sleep(ctx, gap)
	if regs, err := ReadRegisters(ctx, t, p.Address, felicity.BlockVoltage.Start, felicity.BlockVoltage.Count); err == nil {
		packV = regs[0]
	}
	_ = sleep(ctx, gap)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cell1 != 0 {
		p.DetectScale(cell1, packV)
	}
	log.Info("pack scaling", "address", p.Address, "cell_scale", float64(p.CellScale), "voltage_scale", float64(p.VoltageScale))

	// Optional blocks: one attempt each, absence means "not implemented by this firmware".
	if read(felicity.BlockVersion, 1) {
		log.Info("pack version", "address", p.Address,
			"firmware", felicity.VersionString(p.FirmwareVersion), "hardware", felicity.VersionString(p.HardwareVersion))
	}
	if read(felicity.BlockAlarms, 1) {
		log.Info("pack supports alarm registers", "address", p.Address, "flags", p.AlarmFlags())
	}

	// Regular blocks, so the pack is Complete() before discovery is published.
	for _, b := range []felicity.Block{felicity.BlockStatus, felicity.BlockCells, felicity.BlockLimits} {
		if !read(b, 3) {
			log.Warn("pack did not answer during initialisation", "address", p.Address, "block", b.Name)
			p.Online = false
		}
	}
	if p.Complete() {
		p.LastStatusRead = time.Now()
	}
	return ctx.Err()
}

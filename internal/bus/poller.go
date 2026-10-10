package bus

import (
	"context"
	"log/slog"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
)

// Intervals are the per-block polling periods (docs/protocol.md §8).
type Intervals struct {
	Status  time.Duration
	Cells   time.Duration
	Limits  time.Duration
	Alarms  time.Duration
	Offline time.Duration // period for every block while a pack is offline
}

// Poller owns the bus after enumeration. It is the only goroutine that talks to the Transport.
type Poller struct {
	Transport Transport
	Packs     []*felicity.Pack
	Intervals Intervals
	// Gap is the minimum pause after each exchange (docs/protocol.md §1: > 100 ms).
	Gap time.Duration
	// OfflineAfter is the number of consecutive failed status reads that mark a pack offline.
	OfflineAfter int
	// OnUpdate is called after every successful block read, and whenever Online changes.
	OnUpdate func(p *felicity.Pack, block felicity.Block, onlineChanged bool)
	Log      *slog.Logger

	// Now is overridable for tests.
	Now func() time.Time
}

type job struct {
	pack  *felicity.Pack
	block felicity.Block
	due   time.Time
}

// Run polls until ctx is cancelled.
func (pl *Poller) Run(ctx context.Context) error {
	log := logger(pl.Log)
	now := pl.Now
	if now == nil {
		now = time.Now
	}
	if pl.OfflineAfter <= 0 {
		pl.OfflineAfter = 5
	}
	failures := map[*felicity.Pack]int{}

	// Build the job table. Offline packs and never-initialised packs still get the regular blocks.
	var jobs []*job
	for _, p := range pl.Packs {
		jobs = append(jobs,
			&job{pack: p, block: felicity.BlockStatus},
			&job{pack: p, block: felicity.BlockCells},
			&job{pack: p, block: felicity.BlockLimits},
		)
		if p.HasAlarms {
			jobs = append(jobs, &job{pack: p, block: felicity.BlockAlarms})
		}
	}
	// Stagger first runs: status first, then cells, then limits.
	t0 := now()
	for _, j := range jobs {
		j.due = t0.Add(pl.interval(j, true))
	}

	for {
		next := jobs[0]
		for _, j := range jobs[1:] {
			if j.due.Before(next.due) {
				next = j
			}
		}
		if err := sleep(ctx, next.due.Sub(now())); err != nil {
			return err
		}
		p := next.pack
		_, err := ReadBlock(ctx, pl.Transport, p, next.block)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		onlineChanged := false
		if err != nil {
			p.BusErrors++
			log.Debug("read failed", "address", p.Address, "block", next.block.Name, "err", err)
			if next.block.Name == felicity.BlockStatus.Name {
				failures[p]++
				if p.Online && failures[p] >= pl.OfflineAfter {
					p.Online = false
					onlineChanged = true
					log.Warn("pack offline", "address", p.Address, "failures", failures[p])
				}
			}
		} else {
			if next.block.Name == felicity.BlockStatus.Name {
				failures[p] = 0
				p.LastStatusRead = now()
			}
			if !p.Online && p.Complete() {
				p.Online = true
				onlineChanged = true
				log.Info("pack online", "address", p.Address)
			}
		}
		if (err == nil || onlineChanged) && pl.OnUpdate != nil {
			pl.OnUpdate(p, next.block, onlineChanged)
		}
		next.due = now().Add(pl.interval(next, false))
		if err := sleep(ctx, pl.Gap); err != nil {
			return err
		}
	}
}

func (pl *Poller) interval(j *job, initial bool) time.Duration {
	if !j.pack.Online {
		if initial {
			return 0
		}
		return pl.Intervals.Offline
	}
	var d time.Duration
	switch j.block.Name {
	case felicity.BlockStatus.Name:
		d = pl.Intervals.Status
	case felicity.BlockCells.Name:
		d = pl.Intervals.Cells
	case felicity.BlockLimits.Name:
		d = pl.Intervals.Limits
	case felicity.BlockAlarms.Name:
		d = pl.Intervals.Alarms
	}
	if initial {
		// The blocks were just read by Scan; spread the first refresh across the interval so the
		// three blocks do not all come due at once.
		switch j.block.Name {
		case felicity.BlockStatus.Name:
			return d
		case felicity.BlockCells.Name:
			return d / 2
		default:
			return d * 2 / 3
		}
	}
	return d
}

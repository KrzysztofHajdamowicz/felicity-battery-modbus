package bus

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/modbus"
)

const capturePath = "../../captures/2026-10-10-rs485-bus.log"

func openReplay(t *testing.T) *ReplayTransport {
	t.Helper()
	tr, err := OpenReplay(capturePath)
	if err != nil {
		t.Skip(err)
	}
	return tr
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(os.NewFile(0, os.DevNull), nil)) }

func TestReplayExchange(t *testing.T) {
	tr := openReplay(t)
	regs, err := ReadRegisters(context.Background(), tr, 1, 37, 17)
	if err != nil {
		t.Fatal(err)
	}
	if regs[0] != 4 || regs[14] < 90 {
		t.Errorf("regs = %v", regs)
	}
	// Unknown register range -> no response.
	if _, err := ReadRegisters(context.Background(), tr, 1, 1, 6); !errors.Is(err, modbus.ErrNoResponse) {
		t.Errorf("version block: %v", err)
	}
	// Unknown address -> no response.
	if _, err := ReadRegisters(context.Background(), tr, 9, 51, 1); !errors.Is(err, modbus.ErrNoResponse) {
		t.Errorf("address 9: %v", err)
	}
}

func TestScanReplay(t *testing.T) {
	tr := openReplay(t)
	packs, err := Scan(context.Background(), tr, ScanOptions{Gap: time.Millisecond, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 3 {
		t.Fatalf("found %d packs", len(packs))
	}
	for i, p := range packs {
		if p.Address != byte(i+1) || !p.Online || !p.Complete() {
			t.Errorf("pack %d: addr=%d online=%v complete=%v", i, p.Address, p.Online, p.Complete())
		}
		if p.HasVersion || p.HasAlarms {
			t.Errorf("pack %d: optional blocks should be unsupported in the capture", p.Address)
		}
		if p.CellScale != felicity.ScaleMilliVolt || p.VoltageScale != felicity.ScaleMilliVolt {
			t.Errorf("pack %d scale %v %v", p.Address, p.CellScale, p.VoltageScale)
		}
	}

	// Explicit addresses: a missing pack is kept, offline.
	packs, err = Scan(context.Background(), tr, ScanOptions{Addresses: []byte{2, 7}, Gap: time.Millisecond, Log: quiet()})
	if err != nil || len(packs) != 2 || !packs[0].Online || packs[1].Online {
		t.Fatalf("explicit: %v %v", packs, err)
	}

	// Empty bus.
	empty, _ := NewReplay(os.NewFile(0, os.DevNull))
	if _, err := Scan(context.Background(), empty, ScanOptions{Gap: time.Millisecond, MaxAddress: 2, Log: quiet()}); !errors.Is(err, ErrNoPacks) {
		t.Errorf("empty bus: %v", err)
	}
}

// flaky wraps a transport and fails every request to one address after N calls.
type flaky struct {
	Transport
	mu    sync.Mutex
	dead  byte
	calls int
	after int
}

func (f *flaky) Exchange(ctx context.Context, req []byte, n int) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	kill := req[0] == f.dead && f.calls > f.after
	f.mu.Unlock()
	if kill {
		return nil, nil
	}
	return f.Transport.Exchange(ctx, req, n)
}

func TestPollerOfflineOnline(t *testing.T) {
	tr := openReplay(t)
	packs, err := Scan(context.Background(), tr, ScanOptions{Gap: time.Millisecond, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	fl := &flaky{Transport: tr, dead: 2, after: 0}
	var mu sync.Mutex
	updates := map[byte]int{}
	var offline, online int
	ctx, cancel := context.WithCancel(context.Background())
	pl := &Poller{
		Transport:    fl,
		Packs:        packs,
		Intervals:    Intervals{Status: 5 * time.Millisecond, Cells: 20 * time.Millisecond, Limits: 30 * time.Millisecond, Offline: 10 * time.Millisecond},
		Gap:          time.Millisecond,
		OfflineAfter: 3,
		Log:          quiet(),
		OnUpdate: func(p *felicity.Pack, b felicity.Block, changed bool) {
			mu.Lock()
			defer mu.Unlock()
			updates[p.Address]++
			if changed && !p.Online {
				offline++
				// Revive pack 2 once it has been declared offline.
				fl.mu.Lock()
				fl.dead = 0
				fl.mu.Unlock()
			}
			if changed && p.Online {
				online++
				cancel()
			}
		},
	}
	done := make(chan error, 1)
	go func() { done <- pl.Run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("poller did not cycle pack 2 offline and online in time")
	}
	mu.Lock()
	defer mu.Unlock()
	if offline != 1 || online != 1 {
		t.Errorf("offline=%d online=%d", offline, online)
	}
	if updates[1] == 0 || updates[3] == 0 {
		t.Errorf("packs 1 and 3 should keep updating: %v", updates)
	}
	if packs[1].BusErrors < 3 {
		t.Errorf("bus errors on pack 2 = %d", packs[1].BusErrors)
	}
}

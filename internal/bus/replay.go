package bus

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"sync"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/modbus"
)

// ReplayTransport answers requests from a bus capture. For every address it keeps a register image
// that advances through the captured responses one step per request, so values change over time
// like on a live bus. Registers never seen in the capture yield no response, which makes the
// optional blocks (version, alarms) behave like an unsupported feature.
type ReplayTransport struct {
	mu     sync.Mutex
	images map[byte]*replayImage
}

type replayImage struct {
	regs      map[uint16]uint16
	responses [][]uint16 // register slices starting at starts[i]
	starts    []uint16
	next      int
}

// OpenReplay loads a capture file (format of captures/*.log).
func OpenReplay(path string) (*ReplayTransport, error) {
	f, err := os.Open(path) //nolint:gosec // the capture path comes from the command line
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return NewReplay(f)
}

// NewReplay builds a replay transport from capture text.
func NewReplay(r io.Reader) (*ReplayTransport, error) {
	frames, err := modbus.ReadCapture(r)
	if err != nil {
		return nil, err
	}
	t := &ReplayTransport{images: map[byte]*replayImage{}}
	var start uint16
	var addr byte
	for _, fr := range frames {
		if fr.TX {
			if len(fr.Data) >= 6 {
				addr = fr.Data[0]
				start = binary.BigEndian.Uint16(fr.Data[2:])
			}
			continue
		}
		resp, err := modbus.ParseResponse(fr.Data)
		if err != nil || resp.Address != addr {
			continue
		}
		img := t.images[addr]
		if img == nil {
			img = &replayImage{regs: map[uint16]uint16{}}
			t.images[addr] = img
		}
		img.responses = append(img.responses, resp.Registers)
		img.starts = append(img.starts, start)
	}
	// Prime every image with the full capture so the first request already sees every block.
	for _, img := range t.images {
		for i := range img.responses {
			img.apply(i)
		}
	}
	return t, nil
}

func (img *replayImage) apply(i int) {
	for j, v := range img.responses[i] {
		img.regs[img.starts[i]+uint16(j)] = v
	}
}

// Exchange answers a read-holding-registers request from the image, in the Felicity frame format.
func (t *ReplayTransport) Exchange(_ context.Context, request []byte, _ int) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(request) != 8 || request[1] != modbus.FuncReadHolding {
		return nil, nil
	}
	img := t.images[request[0]]
	if img == nil {
		return nil, nil
	}
	if len(img.responses) > 0 {
		img.apply(img.next)
		img.next = (img.next + 1) % len(img.responses)
	}
	start := binary.BigEndian.Uint16(request[2:])
	count := binary.BigEndian.Uint16(request[4:])
	frame := []byte{request[0], modbus.FuncReadHolding, byte(count >> 8), byte(count)}
	for r := start; r < start+count; r++ {
		v, ok := img.regs[r]
		if !ok {
			return nil, nil
		}
		frame = append(frame, byte(v>>8), byte(v))
	}
	crc := modbus.CRC16(frame)
	return append(frame, byte(crc), byte(crc>>8)), nil
}

// Close is a no-op.
func (t *ReplayTransport) Close() error { return nil }

// Addresses lists the pack addresses present in the capture (for tests and -scan).
func (t *ReplayTransport) Addresses() []byte {
	var out []byte
	for a := range t.images {
		out = append(out, a)
	}
	return out
}

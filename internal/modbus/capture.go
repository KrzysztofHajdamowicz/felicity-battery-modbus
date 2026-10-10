package modbus

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// CaptureFrame is one line of a bus capture: "<timestamp> TX|RX <hex bytes>".
type CaptureFrame struct {
	Timestamp string
	TX        bool
	Data      []byte
}

// ReadCapture parses the capture format used in captures/. Blank lines and lines starting with
// '#' are ignored.
func ReadCapture(r io.Reader) ([]CaptureFrame, error) {
	var out []CaptureFrame
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) < 3 {
			return nil, fmt.Errorf("capture line %d: expected '<time> <TX|RX> <hex...>'", line)
		}
		data, err := hex.DecodeString(strings.Join(fields[2:], ""))
		if err != nil {
			return nil, fmt.Errorf("capture line %d: %w", line, err)
		}
		var tx bool
		switch fields[1] {
		case "TX":
			tx = true
		case "RX":
		default:
			return nil, fmt.Errorf("capture line %d: unknown direction %q", line, fields[1])
		}
		out = append(out, CaptureFrame{Timestamp: fields[0], TX: tx, Data: data})
	}
	return out, sc.Err()
}

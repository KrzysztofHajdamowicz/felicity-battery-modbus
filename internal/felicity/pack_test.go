package felicity

import (
	"os"
	"testing"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/modbus"
)

// Worked example from docs/protocol.md §5.
func TestWorkedExample(t *testing.T) {
	p := NewPack(1)
	p.Apply(37, []uint16{4, 3012, 3012, 3012, 3002, 0, 0, 0, 0, 0, 0, 0, 19, 53410, 95, 5, 22320})
	if !p.HasStatus || p.TempSensorCount != 4 {
		t.Fatalf("status block not applied: %+v", p)
	}
	check := func(name string, got, want float64) {
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("voltage", p.Voltage(), 53.41)
	check("current", p.Current(), -1.9)
	check("power", p.Power(), -101)
	check("capacity", p.FullCapacityAh(), 350)
	check("remaining", p.RemainingAh(), 332.5)
	if p.SOC != 95 || p.Charging() {
		t.Errorf("soc=%d charging=%v", p.SOC, p.Charging())
	}
	t1, _ := p.TempC(1)
	t4, _ := p.TempC(4)
	tp, _ := p.Temperature()
	check("t1", t1, 28.1)
	check("t4", t4, 27.1)
	check("temperature", tp, 28.1)
	if _, ok := p.TempC(5); ok {
		t.Error("sensor 5 should be unused")
	}
	if temps := p.Temps(); len(temps) != 4 || temps[3] != 27.1 {
		t.Errorf("temps = %v", temps)
	}
}

func TestLimitsAndFlags(t *testing.T) {
	p := NewPack(2)
	p.Apply(112, []uint16{576, 480, 320, 1600, 0xC0})
	if !p.HasLimits {
		t.Fatal("limits not applied")
	}
	if p.ChargeVoltageLimit() != 57.6 || p.DischargeVoltageLimit() != 48.0 ||
		p.ChargeCurrentLimit() != 32.0 || p.DischargeCurrentLimit() != 160.0 {
		t.Errorf("limits: %v %v %v %v", p.ChargeVoltageLimit(), p.DischargeVoltageLimit(), p.ChargeCurrentLimit(), p.DischargeCurrentLimit())
	}
	f := p.StatusFlags()
	if !f.ChargeEnable || !f.DischargeEnable || f.ChargeImmediately || f.FullChargeRequest || f.SmallCurrentCharge {
		t.Errorf("flags = %+v", f)
	}
	p.Status = 0x30
	if !p.StatusFlags().ChargeImmediately {
		t.Error("bit4|bit5 should set ChargeImmediately")
	}
	if names := p.StatusFlagNames(); len(names) != 2 || names[0] != "charge_immediately_2" {
		t.Errorf("names = %v", names)
	}
}

func TestCellsAndScale(t *testing.T) {
	p := NewPack(1)
	regs := []uint16{16, 3342, 3342, 3343, 3342, 3342, 3342, 3343, 3342, 3342, 3342, 3343, 3343, 3343, 3342, 3342, 3340, 0, 0, 0, 0}
	p.Apply(16, regs)
	p.DetectScale(regs[1], 53410)
	if p.CellScale != ScaleMilliVolt || p.VoltageScale != ScaleMilliVolt {
		t.Errorf("scale %v %v", p.CellScale, p.VoltageScale)
	}
	cells := p.Cells()
	if len(cells) != 16 || cells[0] != 3.342 {
		t.Errorf("cells = %v", cells)
	}
	cs, ok := p.CellStats()
	if !ok || cs.Min != 3.340 || cs.MinIndex != 16 || cs.Max != 3.343 || cs.MaxIndex != 3 || cs.DeltaMilliV != 3 || cs.Avg != 3.342 {
		t.Errorf("stats = %+v", cs)
	}

	// Document scaling (0.1 V).
	q := NewPack(2)
	q.DetectScale(33, 534)
	if q.CellScale != ScaleDeciVolt || q.VoltageScale != ScaleDeciVolt {
		t.Errorf("document scale: %v %v", q.CellScale, q.VoltageScale)
	}
	q.Apply(50, []uint16{534})
	if q.Voltage() != 53.4 {
		t.Errorf("voltage = %v", q.Voltage())
	}
	// mV cells but 0.1 V pack voltage.
	r := NewPack(3)
	r.DetectScale(3342, 534)
	if r.CellScale != ScaleMilliVolt || r.VoltageScale != ScaleDeciVolt {
		t.Errorf("mixed scale: %v %v", r.CellScale, r.VoltageScale)
	}
}

func TestAlarmsAndVersion(t *testing.T) {
	p := NewPack(1)
	p.Apply(54, []uint16{3, 0x3, 0, 0x8000, 0, 0, 0})
	if !p.HasAlarms || !p.HasAnyAlarm() || p.PacksOnline != 3 {
		t.Fatalf("%+v", p)
	}
	want := []string{"charge_alarm:CHT", "charge_alarm:COV", "charge_protect:OCC2"}
	got := p.AlarmFlags()
	if len(got) != len(want) {
		t.Fatalf("flags = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("flag %d = %s, want %s", i, got[i], want[i])
		}
	}
	if f := ChargeProtect2Bits.Flags(1 << 9); len(f) != 1 || f[0] != "bit9" {
		t.Errorf("unnamed bit: %v", f)
	}
	p.Apply(1, []uint16{0, 0, 1, 7, 0, 0})
	if !p.HasVersion || VersionString(p.FirmwareVersion) != "1.7" || VersionString(p.HardwareVersion) != "" {
		t.Errorf("version: fw=%q hw=%q", VersionString(p.FirmwareVersion), VersionString(p.HardwareVersion))
	}
}

// Replays the capture through the packs and checks the end state against the Python decoder.
func TestCaptureReplay(t *testing.T) {
	f, err := os.Open("../../captures/2026-10-10-rs485-bus.log")
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	frames, err := modbus.ReadCapture(f)
	if err != nil {
		t.Fatal(err)
	}
	packs := map[byte]*Pack{}
	var start uint16
	var addr byte
	for _, fr := range frames {
		if fr.TX {
			addr = fr.Data[0]
			start = uint16(fr.Data[2])<<8 | uint16(fr.Data[3])
			continue
		}
		resp, err := modbus.ParseResponse(fr.Data)
		if err != nil || resp.Address != addr {
			continue
		}
		p, ok := packs[addr]
		if !ok {
			p = NewPack(addr)
			packs[addr] = p
		}
		p.Apply(start, resp.Registers)
	}
	if len(packs) != 3 {
		t.Fatalf("expected 3 packs, got %d", len(packs))
	}
	for a, p := range packs {
		if !p.Complete() {
			t.Errorf("pack %d incomplete", a)
		}
		if p.CellCount != 16 || p.TempSensorCount != 4 || p.FullCapacityAh() != 350 {
			t.Errorf("pack %d: cells=%d temps=%d cap=%v", a, p.CellCount, p.TempSensorCount, p.FullCapacityAh())
		}
		if v := p.Voltage(); v < 53 || v > 54 {
			t.Errorf("pack %d voltage %v", a, v)
		}
		if p.DischargeCurrentLimit() != 160 || p.ChargeVoltageLimit() != 57.6 {
			t.Errorf("pack %d limits", a)
		}
	}
	if packs[1].ChargeCurrentLimit() != 48 || packs[2].ChargeCurrentLimit() != 32 || packs[3].ChargeCurrentLimit() != 40 {
		t.Errorf("charge current limits: %v %v %v", packs[1].ChargeCurrentLimit(), packs[2].ChargeCurrentLimit(), packs[3].ChargeCurrentLimit())
	}
}

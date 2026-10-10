package felicity

import (
	"math"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/modbus"
)

// Scale is the multiplier applied to a raw cell / pack voltage register.
type Scale float64

const (
	ScaleMilliVolt  Scale = 0.001 // Felicity FLB48314TG1 ("HD" variant)
	ScaleDeciVolt   Scale = 0.1   // Voltronic document default
	kelvinOffsetTen       = 2731.5
)

// Pack is the last known state of one battery pack. Zero values mean "not read yet"; the Has*
// flags say which blocks have been applied at least once.
type Pack struct {
	Address      byte
	CellScale    Scale
	VoltageScale Scale

	HasVersion bool
	HasCells   bool
	HasStatus  bool
	HasLimits  bool
	HasAlarms  bool // the pack answered the 54..60 block at least once

	ProtocolType    uint16
	ProtocolVersion uint16
	FirmwareVersion uint32
	HardwareVersion uint32

	CellCount uint16
	CellsRaw  [MaxCells]uint16 // index 0 = cell 1

	TempSensorCount uint16
	TempsRaw        [MaxTempSensors]uint16 // index 0 = sensor 1, 0.1 K

	ChargeCurrentRaw    uint16 // 0.1 A
	DischargeCurrentRaw int16  // 0.1 A, use absolute value
	VoltageRaw          uint16
	SOC                 uint16
	FullCapacityRaw     uint32 // mAh

	PacksOnline       uint16
	ChargeAlarm       uint16
	DischargeAlarm    uint16
	ChargeProtect     uint16
	ChargeProtect2    uint16
	DischargeProtect  uint16
	DischargeProtect2 uint16

	ChargeVoltageLimitRaw    uint16 // 0.1 V
	DischargeVoltageLimitRaw uint16 // 0.1 V
	ChargeCurrentLimitRaw    uint16 // 0.1 A
	DischargeCurrentLimitRaw uint16 // 0.1 A
	Status                   uint16

	// Bus bookkeeping maintained by the poller.
	Online         bool
	BusErrors      uint64
	LastUpdate     time.Time
	LastStatusRead time.Time
}

// NewPack returns a pack with the Felicity (mV) scaling, which DetectScale may adjust.
func NewPack(address byte) *Pack {
	return &Pack{Address: address, CellScale: ScaleMilliVolt, VoltageScale: ScaleMilliVolt}
}

// Apply stores a decoded block (start register + registers) into the pack state.
func (p *Pack) Apply(start uint16, regs []uint16) {
	for i := 0; i < len(regs); i++ {
		r := int(start) + i
		v := regs[i]
		next := func() (uint16, bool) {
			if i+1 < len(regs) {
				return regs[i+1], true
			}
			return 0, false
		}
		switch {
		case r == RegProtocolType:
			p.ProtocolType = v
			p.HasVersion = true
		case r == RegProtocolVersion:
			p.ProtocolVersion = v
		case r == RegFirmwareVersion:
			if lo, ok := next(); ok {
				p.FirmwareVersion = modbus.U32(v, lo)
				i++
			}
		case r == RegHardwareVersion:
			if lo, ok := next(); ok {
				p.HardwareVersion = modbus.U32(v, lo)
				i++
			}
		case r == RegCellCount:
			p.CellCount = v
			p.HasCells = true
		case r >= RegCellVoltageFirst && r <= RegCellVoltageLast:
			p.CellsRaw[r-RegCellVoltageFirst] = v
		case r == RegTempSensorCount:
			p.TempSensorCount = v
			p.HasStatus = true
		case r >= RegTempFirst && r <= RegTempLast:
			p.TempsRaw[r-RegTempFirst] = v
		case r == RegChargeCurrent:
			p.ChargeCurrentRaw = v
		case r == RegDischargeCurrent:
			p.DischargeCurrentRaw = modbus.S16(v)
		case r == RegPackVoltage:
			p.VoltageRaw = v
		case r == RegSOC:
			p.SOC = v
		case r == RegFullCapacity:
			if lo, ok := next(); ok {
				p.FullCapacityRaw = modbus.U32(v, lo)
				i++
			}
		case r == RegPacksOnline:
			p.PacksOnline = v
			p.HasAlarms = true
		case r == RegChargeAlarm:
			p.ChargeAlarm = v
		case r == RegDischargeAlarm:
			p.DischargeAlarm = v
		case r == RegChargeProtect:
			p.ChargeProtect = v
		case r == RegChargeProtect2:
			p.ChargeProtect2 = v
		case r == RegDischargeProtect:
			p.DischargeProtect = v
		case r == RegDischargeProtect2:
			p.DischargeProtect2 = v
		case r == RegChargeVoltLimit:
			p.ChargeVoltageLimitRaw = v
			p.HasLimits = true
		case r == RegDischargeVoltLim:
			p.DischargeVoltageLimitRaw = v
		case r == RegChargeCurrLimit:
			p.ChargeCurrentLimitRaw = v
		case r == RegDischargeCurrLim:
			p.DischargeCurrentLimitRaw = v
		case r == RegStatus:
			p.Status = v
		}
	}
}

// DetectScale implements docs/protocol.md §6 step 2 from the 16..20 block and register 50.
// cell1 is the raw cell 1 voltage, packVoltage the raw register 50 value.
func (p *Pack) DetectScale(cell1, packVoltage uint16) {
	if cell1 > 1000 {
		p.CellScale = ScaleMilliVolt
		if packVoltage >= 10000 {
			p.VoltageScale = ScaleMilliVolt
		} else {
			p.VoltageScale = ScaleDeciVolt
		}
	} else {
		p.CellScale = ScaleDeciVolt
		p.VoltageScale = ScaleDeciVolt
	}
}

// Complete reports whether the three regular blocks have all been read at least once.
func (p *Pack) Complete() bool { return p.HasCells && p.HasStatus && p.HasLimits }

// ---- derived values (docs/protocol.md §5)

// Voltage in volts.
func (p *Pack) Voltage() float64 { return round(float64(p.VoltageRaw)*float64(p.VoltageScale), 3) }

// ChargeCurrent in amperes (≥ 0).
func (p *Pack) ChargeCurrent() float64 { return round(float64(p.ChargeCurrentRaw)*0.1, 1) }

// DischargeCurrent in amperes (≥ 0).
func (p *Pack) DischargeCurrent() float64 {
	return round(math.Abs(float64(p.DischargeCurrentRaw))*0.1, 1)
}

// Current in amperes, positive when charging.
func (p *Pack) Current() float64 { return round(p.ChargeCurrent()-p.DischargeCurrent(), 1) }

// Power in watts, positive when charging.
func (p *Pack) Power() float64 { return math.Round(p.Current() * p.Voltage()) }

// FullCapacityAh is the full-charge capacity in Ah.
func (p *Pack) FullCapacityAh() float64 { return round(float64(p.FullCapacityRaw)*0.001, 3) }

// RemainingAh estimates the remaining capacity from SOC and full-charge capacity.
func (p *Pack) RemainingAh() float64 {
	return round(p.FullCapacityAh()*float64(p.SOC)/100, 1)
}

// Charging reports whether charge current is flowing.
func (p *Pack) Charging() bool { return p.ChargeCurrentRaw > 0 }

// TempC returns the temperature of sensor n (1-based) in °C, and false if the sensor is unused.
func (p *Pack) TempC(n int) (float64, bool) {
	if n < 1 || n > MaxTempSensors {
		return 0, false
	}
	raw := p.TempsRaw[n-1]
	if raw == 0 {
		return 0, false
	}
	tenths := float64(raw) - kelvinOffsetTen
	if raw <= 1000 {
		tenths = float64(raw)*10 - kelvinOffsetTen
	}
	return roundHalfUp(tenths/10, 1), true
}

// Temperature is the pack temperature: mean of sensors 1..3 (sensor 4 on FLB48314TG1 sits
// elsewhere and reads ~1 K lower). Falls back to the mean of all used sensors.
func (p *Pack) Temperature() (float64, bool) {
	sum, n := 0.0, 0
	for i := 1; i <= 3; i++ {
		if t, ok := p.TempC(i); ok {
			sum += t
			n++
		}
	}
	if n == 0 {
		for i := 4; i <= MaxTempSensors; i++ {
			if t, ok := p.TempC(i); ok {
				sum += t
				n++
			}
		}
	}
	if n == 0 {
		return 0, false
	}
	return roundHalfUp(sum/float64(n), 1), true
}

// Temps returns the temperatures of the sensors 1..TempSensorCount (NaN for unused sensors).
func (p *Pack) Temps() []float64 {
	n := int(p.TempSensorCount)
	if n > MaxTempSensors {
		n = MaxTempSensors
	}
	out := make([]float64, n)
	for i := range out {
		if t, ok := p.TempC(i + 1); ok {
			out[i] = t
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}

// Cells returns the voltages of cells 1..CellCount in volts.
func (p *Pack) Cells() []float64 {
	n := int(p.CellCount)
	if n > MaxCells {
		n = MaxCells
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = round(float64(p.CellsRaw[i])*float64(p.CellScale), 3)
	}
	return out
}

// CellStats summarises the cells with a non-zero reading.
type CellStats struct {
	Min, Max, Avg float64
	MinIndex      int // 1-based
	MaxIndex      int // 1-based
	DeltaMilliV   float64
}

// CellStats returns min / max / average over the populated cells, and false if none are populated.
func (p *Pack) CellStats() (CellStats, bool) {
	var s CellStats
	sum, n := 0.0, 0
	for i, v := range p.Cells() {
		if v <= 0 {
			continue
		}
		if n == 0 || v < s.Min {
			s.Min, s.MinIndex = v, i+1
		}
		if n == 0 || v > s.Max {
			s.Max, s.MaxIndex = v, i+1
		}
		sum += v
		n++
	}
	if n == 0 {
		return s, false
	}
	s.Avg = round(sum/float64(n), 3)
	s.DeltaMilliV = math.Round((s.Max - s.Min) * 1000)
	return s, true
}

// Limits in engineering units.
func (p *Pack) ChargeVoltageLimit() float64 { return round(float64(p.ChargeVoltageLimitRaw)*0.1, 1) }
func (p *Pack) DischargeVoltageLimit() float64 {
	return round(float64(p.DischargeVoltageLimitRaw)*0.1, 1)
}
func (p *Pack) ChargeCurrentLimit() float64 { return round(float64(p.ChargeCurrentLimitRaw)*0.1, 1) }
func (p *Pack) DischargeCurrentLimit() float64 {
	return round(float64(p.DischargeCurrentLimitRaw)*0.1, 1)
}

// StatusFlags decodes register 0x74.
type StatusFlags struct {
	ChargeEnable       bool `json:"charge_enabled"`
	DischargeEnable    bool `json:"discharge_enabled"`
	ChargeImmediately  bool `json:"charge_immediately"` // bit5 (SOC ≤ 9 %) or bit4 (SOC 10–14 %)
	FullChargeRequest  bool `json:"full_charge_request"`
	SmallCurrentCharge bool `json:"small_current_charge_request"`
}

func (p *Pack) StatusFlags() StatusFlags {
	v := p.Status
	return StatusFlags{
		ChargeEnable:       v&StatusChargeEnable != 0,
		DischargeEnable:    v&StatusDischargeEnable != 0,
		ChargeImmediately:  v&(StatusChargeImmediately|StatusChargeImmediately2) != 0,
		FullChargeRequest:  v&StatusFullChargeRequest != 0,
		SmallCurrentCharge: v&StatusSmallCurrentCharge != 0,
	}
}

// StatusFlagNames lists the active 0x74 flags by name.
func (p *Pack) StatusFlagNames() []string {
	names := BitName{7: "charge_enable", 6: "discharge_enable", 5: "charge_immediately",
		4: "charge_immediately_2", 3: "full_charge_request", 2: "small_current_charge_request"}
	return names.Flags(p.Status)
}

// AlarmFlags lists the active alarm / protection bits with the register they come from.
func (p *Pack) AlarmFlags() []string {
	var out []string
	add := func(prefix string, names BitName, v uint16) {
		for _, f := range names.Flags(v) {
			out = append(out, prefix+":"+f)
		}
	}
	add("charge_alarm", ChargeAlarmBits, p.ChargeAlarm)
	add("discharge_alarm", DischargeAlarmBits, p.DischargeAlarm)
	add("charge_protect", ChargeProtectBits, p.ChargeProtect)
	add("charge_protect2", ChargeProtect2Bits, p.ChargeProtect2)
	add("discharge_protect", DischargeProtectBits, p.DischargeProtect)
	add("discharge_protect2", DischargeProtect2Bits, p.DischargeProtect2)
	return out
}

// HasAnyAlarm reports any non-zero alarm or protection register.
func (p *Pack) HasAnyAlarm() bool {
	return p.ChargeAlarm|p.DischargeAlarm|p.ChargeProtect|p.ChargeProtect2|p.DischargeProtect|p.DischargeProtect2 != 0
}

// VersionString formats a u32 version as "major.minor" (high word.low word), or "" when zero.
func VersionString(v uint32) string {
	if v == 0 {
		return ""
	}
	return itoa(int(v>>16)) + "." + itoa(int(v&0xffff))
}

func round(x float64, digits int) float64 {
	m := math.Pow10(digits)
	return math.Round(x*m) / m
}

// roundHalfUp rounds away from the Python decoder's banker's-rounding pitfalls: 28.05 -> 28.1.
func roundHalfUp(x float64, digits int) float64 {
	m := math.Pow10(digits)
	return math.Floor(x*m+0.5+1e-9) / m
}

package ha

import (
	"encoding/json"
	"math"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
)

// PackState is the JSON document published on <prefix>/pack/<addr>/state. Field names are the
// keys referenced by the discovery value templates; do not rename them.
type PackState struct {
	Address          int      `json:"address"`
	Voltage          float64  `json:"voltage"`
	Current          float64  `json:"current"`
	Power            float64  `json:"power"`
	SOC              int      `json:"soc"`
	Temperature      *float64 `json:"temperature"`
	CapacityAh       float64  `json:"capacity_ah"`
	RemainingAh      float64  `json:"remaining_ah"`
	Charging         bool     `json:"charging"`
	ChargeCurrent    float64  `json:"charge_current"`
	DischargeCurrent float64  `json:"discharge_current"`

	Cells        []float64 `json:"cells"`
	CellCount    int       `json:"cell_count"`
	CellMin      float64   `json:"cell_min"`
	CellMax      float64   `json:"cell_max"`
	CellAvg      float64   `json:"cell_avg"`
	CellDeltaMV  float64   `json:"cell_delta_mv"`
	CellMinIndex int       `json:"cell_min_index"`
	CellMaxIndex int       `json:"cell_max_index"`

	Temps           []*float64 `json:"temps"`
	TempSensorCount int        `json:"temp_sensor_count"`

	ChargeVoltageLimit    float64 `json:"charge_voltage_limit"`
	DischargeVoltageLimit float64 `json:"discharge_voltage_limit"`
	ChargeCurrentLimit    float64 `json:"charge_current_limit"`
	DischargeCurrentLimit float64 `json:"discharge_current_limit"`

	StatusRaw       int      `json:"status_raw"`
	StatusFlagNames []string `json:"status_flags"`
	felicity.StatusFlags

	Alarms *AlarmState `json:"alarms,omitempty"`

	CellScale    float64 `json:"cell_scale"`
	VoltageScale float64 `json:"voltage_scale"`
	Firmware     string  `json:"firmware,omitempty"`
	Hardware     string  `json:"hardware,omitempty"`
	BusErrors    uint64  `json:"bus_errors"`
	LastUpdate   string  `json:"last_update"`
}

// AlarmState mirrors registers 54..60 (only present when the pack implements them).
type AlarmState struct {
	PacksOnline          int      `json:"packs_online"`
	ChargeAlarmRaw       int      `json:"charge_alarm_raw"`
	DischargeAlarmRaw    int      `json:"discharge_alarm_raw"`
	ChargeProtectRaw     int      `json:"charge_protect_raw"`
	ChargeProtect2Raw    int      `json:"charge_protect2_raw"`
	DischargeProtectRaw  int      `json:"discharge_protect_raw"`
	DischargeProtect2Raw int      `json:"discharge_protect2_raw"`
	ChargeAlarm          bool     `json:"charge_alarm"`
	DischargeAlarm       bool     `json:"discharge_alarm"`
	ChargeProtection     bool     `json:"charge_protection"`
	DischargeProtection  bool     `json:"discharge_protection"`
	Flags                []string `json:"flags"`
}

// BuildPackState snapshots a pack.
func BuildPackState(p *felicity.Pack, now time.Time) PackState {
	s := PackState{
		Address:          int(p.Address),
		Voltage:          p.Voltage(),
		Current:          p.Current(),
		Power:            p.Power(),
		SOC:              int(p.SOC),
		CapacityAh:       p.FullCapacityAh(),
		RemainingAh:      p.RemainingAh(),
		Charging:         p.Charging(),
		ChargeCurrent:    p.ChargeCurrent(),
		DischargeCurrent: p.DischargeCurrent(),
		Cells:            p.Cells(),
		CellCount:        int(p.CellCount),
		TempSensorCount:  int(p.TempSensorCount),

		ChargeVoltageLimit:    p.ChargeVoltageLimit(),
		DischargeVoltageLimit: p.DischargeVoltageLimit(),
		ChargeCurrentLimit:    p.ChargeCurrentLimit(),
		DischargeCurrentLimit: p.DischargeCurrentLimit(),
		StatusRaw:             int(p.Status),
		StatusFlagNames:       nonNil(p.StatusFlagNames()),
		CellScale:             float64(p.CellScale),
		VoltageScale:          float64(p.VoltageScale),
		Firmware:              felicity.VersionString(p.FirmwareVersion),
		Hardware:              felicity.VersionString(p.HardwareVersion),
		BusErrors:             p.BusErrors,
		LastUpdate:            now.UTC().Format(time.RFC3339),
	}
	s.StatusFlags = p.StatusFlags()
	if t, ok := p.Temperature(); ok {
		s.Temperature = &t
	}
	for _, t := range p.Temps() {
		if math.IsNaN(t) {
			s.Temps = append(s.Temps, nil)
		} else {
			v := t
			s.Temps = append(s.Temps, &v)
		}
	}
	if s.Temps == nil {
		s.Temps = []*float64{}
	}
	if s.Cells == nil {
		s.Cells = []float64{}
	}
	if cs, ok := p.CellStats(); ok {
		s.CellMin, s.CellMax, s.CellAvg = cs.Min, cs.Max, cs.Avg
		s.CellDeltaMV = cs.DeltaMilliV
		s.CellMinIndex, s.CellMaxIndex = cs.MinIndex, cs.MaxIndex
	}
	if p.HasAlarms {
		s.Alarms = &AlarmState{
			PacksOnline:          int(p.PacksOnline),
			ChargeAlarmRaw:       int(p.ChargeAlarm),
			DischargeAlarmRaw:    int(p.DischargeAlarm),
			ChargeProtectRaw:     int(p.ChargeProtect),
			ChargeProtect2Raw:    int(p.ChargeProtect2),
			DischargeProtectRaw:  int(p.DischargeProtect),
			DischargeProtect2Raw: int(p.DischargeProtect2),
			ChargeAlarm:          p.ChargeAlarm != 0,
			DischargeAlarm:       p.DischargeAlarm != 0,
			ChargeProtection:     p.ChargeProtect|p.ChargeProtect2 != 0,
			DischargeProtection:  p.DischargeProtect|p.DischargeProtect2 != 0,
			Flags:                nonNil(p.AlarmFlags()),
		}
	}
	return s
}

// SystemState is the JSON document published on <prefix>/system/state.
type SystemState struct {
	PacksConfigured []int `json:"packs_configured"`
	PacksOnline     []int `json:"packs_online"`
	PacksOnlineN    int   `json:"packs_online_count"`

	Voltage     float64  `json:"voltage"`
	Current     float64  `json:"current"`
	Power       float64  `json:"power"`
	SOC         float64  `json:"soc"`
	Temperature *float64 `json:"temperature"`
	TempMax     *float64 `json:"temperature_max"`
	TempMaxPack int      `json:"temperature_max_pack"`
	TempMaxIdx  int      `json:"temperature_max_sensor"`

	CapacityAh  float64 `json:"capacity_ah"`
	RemainingAh float64 `json:"remaining_ah"`
	StoredKWh   float64 `json:"stored_kwh"`

	ChargeCurrentLimit    float64 `json:"charge_current_limit"`
	DischargeCurrentLimit float64 `json:"discharge_current_limit"`
	ChargeVoltageLimit    float64 `json:"charge_voltage_limit"`
	DischargeVoltageLimit float64 `json:"discharge_voltage_limit"`

	CellMin      *float64 `json:"cell_min"`
	CellMax      *float64 `json:"cell_max"`
	CellDeltaMV  *float64 `json:"cell_delta_mv"`
	CellMinPack  int      `json:"cell_min_pack"`
	CellMinIndex int      `json:"cell_min_index"`
	CellMaxPack  int      `json:"cell_max_pack"`
	CellMaxIndex int      `json:"cell_max_index"`

	Charging          bool     `json:"charging"`
	ChargeEnabled     bool     `json:"charge_enabled"`
	DischargeEnabled  bool     `json:"discharge_enabled"`
	ChargeImmediately bool     `json:"charge_immediately"`
	Problem           bool     `json:"problem"`
	AlarmFlags        []string `json:"alarm_flags"`
	BusErrors         uint64   `json:"bus_errors"`
	LastUpdate        string   `json:"last_update"`
}

// BuildSystemState snapshots the aggregate.
func BuildSystemState(s felicity.System, now time.Time) SystemState {
	out := SystemState{
		PacksConfigured:       nonNilInts(s.PacksConfigured),
		PacksOnline:           nonNilInts(s.PacksOnline),
		PacksOnlineN:          len(s.PacksOnline),
		Voltage:               s.Voltage,
		Current:               s.Current,
		Power:                 s.Power,
		SOC:                   s.SOC,
		TempMaxPack:           s.TempMaxPack,
		TempMaxIdx:            s.TempMaxIndex,
		CapacityAh:            s.FullCapacityAh,
		RemainingAh:           s.RemainingAh,
		StoredKWh:             s.StoredKWh,
		ChargeCurrentLimit:    s.ChargeCurrentLimit,
		DischargeCurrentLimit: s.DischargeCurrentLimit,
		ChargeVoltageLimit:    s.ChargeVoltageLimit,
		DischargeVoltageLimit: s.DischargeVoltageLimit,
		CellMinPack:           s.CellMinPack,
		CellMinIndex:          s.CellMinIndex,
		CellMaxPack:           s.CellMaxPack,
		CellMaxIndex:          s.CellMaxIndex,
		Charging:              s.Charging,
		ChargeEnabled:         s.ChargeEnabled,
		DischargeEnabled:      s.DischargeEnabled,
		ChargeImmediately:     s.ChargeImmediately,
		Problem:               s.Problem,
		AlarmFlags:            nonNil(s.AlarmFlags),
		BusErrors:             s.BusErrors,
		LastUpdate:            now.UTC().Format(time.RFC3339),
	}
	if len(s.PacksOnline) > 0 {
		t := s.Temperature
		out.Temperature = &t
	}
	if s.HasTemp {
		t := s.TempMax
		out.TempMax = &t
	}
	if s.HasCells {
		mn, mx, d := s.CellMin, s.CellMax, s.CellDeltaMilliV
		out.CellMin, out.CellMax, out.CellDeltaMV = &mn, &mx, &d
	}
	return out
}

// Marshal encodes a state document.
func Marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // the state structs contain only JSON-encodable fields
	}
	return b
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilInts(s []int) []int {
	if s == nil {
		return []int{}
	}
	return s
}

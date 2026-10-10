package felicity

import "math"

// System is the aggregate of the online, complete packs on one bus (docs/protocol.md,
// "one HA device per pack plus a system total").
type System struct {
	PacksConfigured []int
	PacksOnline     []int

	Voltage      float64 // mean
	Current      float64 // sum, positive = charging
	Power        float64 // sum
	SOC          float64 // capacity-weighted mean
	Temperature  float64 // mean of pack temperatures
	TempMax      float64
	TempMaxPack  int
	TempMaxIndex int
	HasTemp      bool

	FullCapacityAh float64
	RemainingAh    float64
	StoredKWh      float64

	ChargeCurrentLimit    float64 // sum over packs with charge enabled
	DischargeCurrentLimit float64 // sum over packs with discharge enabled
	ChargeVoltageLimit    float64 // min
	DischargeVoltageLimit float64 // max

	CellMin, CellMax float64
	CellMinPack      int
	CellMinIndex     int
	CellMaxPack      int
	CellMaxIndex     int
	CellDeltaMilliV  float64
	HasCells         bool

	Charging          bool
	ChargeEnabled     bool // all online packs
	DischargeEnabled  bool // all online packs
	ChargeImmediately bool // any
	AlarmsSupported   bool
	Problem           bool // any alarm / protection, or a configured pack offline
	AlarmFlags        []string
	BusErrors         uint64
}

// Aggregate builds the system view. nominalVoltage is used for the stored-energy estimate.
func Aggregate(packs []*Pack, nominalVoltage float64) System {
	s := System{ChargeEnabled: true, DischargeEnabled: true}
	var vSum, tSum, socWeighted float64
	var nOnline, nTemp int
	for _, p := range packs {
		s.PacksConfigured = append(s.PacksConfigured, int(p.Address))
		s.BusErrors += p.BusErrors
		if !p.Online || !p.Complete() {
			s.Problem = true
			continue
		}
		s.PacksOnline = append(s.PacksOnline, int(p.Address))
		nOnline++
		vSum += p.Voltage()
		s.Current += p.Current()
		s.Power += p.Power()
		cap := p.FullCapacityAh()
		s.FullCapacityAh += cap
		s.RemainingAh += p.RemainingAh()
		socWeighted += float64(p.SOC) * cap
		if t, ok := p.Temperature(); ok {
			tSum += t
			nTemp++
		}
		for i := 1; i <= int(p.TempSensorCount) && i <= MaxTempSensors; i++ {
			if t, ok := p.TempC(i); ok && (!s.HasTemp || t > s.TempMax) {
				s.TempMax, s.TempMaxPack, s.TempMaxIndex, s.HasTemp = t, int(p.Address), i, true
			}
		}
		f := p.StatusFlags()
		if f.ChargeEnable {
			s.ChargeCurrentLimit += p.ChargeCurrentLimit()
		} else {
			s.ChargeEnabled = false
		}
		if f.DischargeEnable {
			s.DischargeCurrentLimit += p.DischargeCurrentLimit()
		} else {
			s.DischargeEnabled = false
		}
		s.ChargeImmediately = s.ChargeImmediately || f.ChargeImmediately
		if nOnline == 1 || p.ChargeVoltageLimit() < s.ChargeVoltageLimit {
			s.ChargeVoltageLimit = p.ChargeVoltageLimit()
		}
		if nOnline == 1 || p.DischargeVoltageLimit() > s.DischargeVoltageLimit {
			s.DischargeVoltageLimit = p.DischargeVoltageLimit()
		}
		if cs, ok := p.CellStats(); ok {
			if !s.HasCells || cs.Min < s.CellMin {
				s.CellMin, s.CellMinPack, s.CellMinIndex = cs.Min, int(p.Address), cs.MinIndex
			}
			if !s.HasCells || cs.Max > s.CellMax {
				s.CellMax, s.CellMaxPack, s.CellMaxIndex = cs.Max, int(p.Address), cs.MaxIndex
			}
			s.HasCells = true
		}
		if p.HasAlarms {
			s.AlarmsSupported = true
			if p.HasAnyAlarm() {
				s.Problem = true
				for _, f := range p.AlarmFlags() {
					s.AlarmFlags = append(s.AlarmFlags, "pack"+itoa(int(p.Address))+":"+f)
				}
			}
		}
	}
	if nOnline == 0 {
		s.ChargeEnabled, s.DischargeEnabled = false, false
		return s
	}
	s.Voltage = round(vSum/float64(nOnline), 3)
	s.Current = round(s.Current, 1)
	s.Power = math.Round(s.Power)
	if s.FullCapacityAh > 0 {
		s.SOC = round(socWeighted/s.FullCapacityAh, 1)
	}
	s.FullCapacityAh = round(s.FullCapacityAh, 3)
	s.RemainingAh = round(s.RemainingAh, 1)
	s.StoredKWh = round(s.RemainingAh*nominalVoltage/1000, 2)
	s.ChargeCurrentLimit = round(s.ChargeCurrentLimit, 1)
	s.DischargeCurrentLimit = round(s.DischargeCurrentLimit, 1)
	if nTemp > 0 {
		s.Temperature = roundHalfUp(tSum/float64(nTemp), 1)
	}
	if s.HasCells {
		s.CellDeltaMilliV = math.Round((s.CellMax - s.CellMin) * 1000)
	}
	s.Charging = s.Current > 0
	return s
}

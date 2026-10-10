package felicity

import "testing"

func mkPack(addr byte, soc uint16, cap uint32, chgLimit, dchLimit, chgV, dchV uint16, status uint16, cells []uint16) *Pack {
	p := NewPack(addr)
	p.Online = true
	regs := []uint16{4, 3012, 3012, 3012, 3002, 0, 0, 0, 0, 0, 0, 0, 19, 53410, soc, uint16(cap >> 16), uint16(cap)}
	p.Apply(37, regs)
	p.Apply(112, []uint16{chgV, dchV, chgLimit, dchLimit, status})
	c := append([]uint16{uint16(len(cells))}, cells...)
	for len(c) < 21 {
		c = append(c, 0)
	}
	p.Apply(16, c)
	return p
}

func TestAggregate(t *testing.T) {
	p1 := mkPack(1, 95, 350000, 480, 1600, 576, 480, 0xC0, []uint16{3342, 3340, 3344})
	p2 := mkPack(2, 96, 350000, 320, 1600, 575, 481, 0xC0, []uint16{3341, 3345, 3343})
	p3 := mkPack(3, 50, 100000, 400, 1600, 576, 480, 0x40, []uint16{3300, 3300, 3300}) // charge disabled
	p3.ChargeCurrentRaw = 100                                                          // 10 A charging
	off := NewPack(4)                                                                  // configured but offline
	s := Aggregate([]*Pack{p1, p2, p3, off}, 51.2)

	if len(s.PacksOnline) != 3 || len(s.PacksConfigured) != 4 || !s.Problem {
		t.Errorf("online=%v configured=%v problem=%v", s.PacksOnline, s.PacksConfigured, s.Problem)
	}
	if s.Voltage != 53.41 {
		t.Errorf("voltage %v", s.Voltage)
	}
	// currents: -1.9, -1.9, +8.1 = 4.3 A -> charging
	if s.Current != 4.3 || !s.Charging {
		t.Errorf("current %v charging %v", s.Current, s.Charging)
	}
	// weighted SOC: (95*350 + 96*350 + 50*100) / 800 = 89.8
	if s.SOC != 89.8 {
		t.Errorf("soc %v", s.SOC)
	}
	if s.FullCapacityAh != 800 || s.RemainingAh != 718.5 || s.StoredKWh != 36.79 {
		t.Errorf("capacity %v remaining %v kwh %v", s.FullCapacityAh, s.RemainingAh, s.StoredKWh)
	}
	// charge limit only over packs with charge enabled: 48 + 32
	if s.ChargeCurrentLimit != 80 || s.DischargeCurrentLimit != 480 {
		t.Errorf("limits %v %v", s.ChargeCurrentLimit, s.DischargeCurrentLimit)
	}
	if s.ChargeVoltageLimit != 57.5 || s.DischargeVoltageLimit != 48.1 {
		t.Errorf("voltage limits %v %v", s.ChargeVoltageLimit, s.DischargeVoltageLimit)
	}
	if s.ChargeEnabled || !s.DischargeEnabled {
		t.Errorf("enabled %v %v", s.ChargeEnabled, s.DischargeEnabled)
	}
	if s.CellMin != 3.3 || s.CellMinPack != 3 || s.CellMinIndex != 1 || s.CellMax != 3.345 || s.CellMaxPack != 2 || s.CellMaxIndex != 2 || s.CellDeltaMilliV != 45 {
		t.Errorf("cells %+v", s)
	}
	if s.Temperature != 28.1 || s.TempMax != 28.1 || s.TempMaxPack != 1 || s.TempMaxIndex != 1 {
		t.Errorf("temps %v %v pack %d idx %d", s.Temperature, s.TempMax, s.TempMaxPack, s.TempMaxIndex)
	}
	if s.AlarmsSupported {
		t.Error("alarms should not be supported")
	}

	empty := Aggregate([]*Pack{off}, 51.2)
	if len(empty.PacksOnline) != 0 || empty.ChargeEnabled || !empty.Problem {
		t.Errorf("empty: %+v", empty)
	}
}

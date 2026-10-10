// Package felicity models one Felicity Solar battery pack (Voltronic "LIB" register map) and the
// aggregate of several packs on one bus. Register numbers and scaling follow docs/protocol.md §4–§7.
package felicity

// Register addresses (decimal, as in docs/protocol.md §4).
const (
	RegProtocolType      = 1
	RegProtocolVersion   = 2
	RegFirmwareVersion   = 3 // u32, 3–4
	RegHardwareVersion   = 5 // u32, 5–6
	RegCellCount         = 16
	RegCellVoltageFirst  = 17 // 17–36, 20 cells
	RegCellVoltageLast   = 36
	RegTempSensorCount   = 37
	RegTempFirst         = 38 // 38–47, 10 sensors
	RegTempLast          = 47
	RegChargeCurrent     = 48
	RegDischargeCurrent  = 49
	RegPackVoltage       = 50
	RegSOC               = 51
	RegFullCapacity      = 52 // u32, 52–53
	RegPacksOnline       = 54
	RegChargeAlarm       = 55
	RegDischargeAlarm    = 56
	RegChargeProtect     = 57
	RegChargeProtect2    = 58
	RegDischargeProtect  = 59
	RegDischargeProtect2 = 60
	RegBMSState          = 61
	RegDesignCapacity    = 62 // u32, 62–63
	RegChargeVoltLimit   = 112
	RegDischargeVoltLim  = 113
	RegChargeCurrLimit   = 114
	RegDischargeCurrLim  = 115
	RegStatus            = 116
	RegRunTimeToEmpty    = 117
	RegRemainingCapacity = 118 // u32, 118–119

	MaxCells       = 20
	MaxTempSensors = 10
	MOSFETSensor   = 6 // per the Voltronic document, sensor 6 is the MOSFET temperature
)

// Block is a contiguous register range read with one request.
type Block struct {
	Name  string
	Start uint16
	Count uint16
}

// The polling blocks from docs/protocol.md §8.
var (
	BlockVersion = Block{"version", RegProtocolType, 6}    // 1..6
	BlockCells   = Block{"cells", RegCellCount, 21}        // 16..36
	BlockStatus  = Block{"status", RegTempSensorCount, 17} // 37..53
	BlockAlarms  = Block{"alarms", RegPacksOnline, 7}      // 54..60
	BlockLimits  = Block{"limits", RegChargeVoltLimit, 5}  // 112..116
	BlockProbe   = Block{"probe", RegSOC, 1}               // 51, used for enumeration
	BlockScale   = Block{"scale", RegCellCount, 5}         // 16..20, used for scaling detection
	BlockVoltage = Block{"voltage", RegPackVoltage, 1}     // 50, used for scaling detection
)

// Status register 0x74 bits (docs/protocol.md §7).
const (
	StatusChargeEnable       = 1 << 7
	StatusDischargeEnable    = 1 << 6
	StatusChargeImmediately  = 1 << 5
	StatusChargeImmediately2 = 1 << 4
	StatusFullChargeRequest  = 1 << 3
	StatusSmallCurrentCharge = 1 << 2
)

// BitName maps a bit index to a flag name for the alarm / protection registers.
type BitName map[uint]string

var (
	ChargeAlarmBits = BitName{0: "CHT", 1: "COV", 2: "CLT", 3: "OCC"}

	DischargeAlarmBits = BitName{0: "DHT", 1: "DLT", 2: "FETHT", 3: "CUV"}

	ChargeProtectBits = BitName{0: "PRES", 1: "BoostNRDY", 2: "AFEComm", 3: "AFEOCC", 4: "AFEOCD",
		5: "AFESCD", 6: "FETHT", 7: "SUV", 8: "CHT", 9: "CLT", 10: "OCV", 11: "OCC", 12: "2NDOVP",
		13: "CANID", 15: "OCC2"}

	ChargeProtect2Bits = BitName{0: "ShutdownByCmd", 1: "Shutdown", 2: "OCCHw", 3: "IDError"}

	DischargeProtectBits = BitName{0: "PRES", 1: "BoostNRDY", 2: "AFEOCC", 3: "AFEOCD", 4: "AFESCD",
		6: "IDError", 10: "FETHT", 11: "CUV", 12: "OCD", 13: "DLT", 14: "DHT", 15: "Shutdown"}

	DischargeProtect2Bits = BitName{0: "AFEComm", 1: "Short", 2: "OCD2", 4: "2NDOVP", 5: "DHT2",
		6: "ShutdownByCmd", 13: "CANID"}
)

// Flags returns the names of the set bits in v, in bit order. Unnamed bits are reported as "bitN".
func (bn BitName) Flags(v uint16) []string {
	var out []string
	for i := uint(0); i < 16; i++ {
		if v&(1<<i) == 0 {
			continue
		}
		if name, ok := bn[i]; ok {
			out = append(out, name)
		} else {
			out = append(out, "bit"+itoa(int(i)))
		}
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [4]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

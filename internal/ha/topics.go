// Package ha builds the MQTT payloads for Home Assistant: the JSON state documents and the
// device-based discovery payloads (one HA device per pack, one for the system).
package ha

import "fmt"

// Topics derives every MQTT topic from the configured prefixes.
type Topics struct {
	Prefix          string // state topics, e.g. "felicity"
	DiscoveryPrefix string // e.g. "homeassistant"
	SystemID        string // unique_id / identifier prefix, e.g. "felicity"
}

func (t Topics) Availability() string { return t.Prefix + "/availability" }
func (t Topics) PackAvailability(addr byte) string {
	return fmt.Sprintf("%s/pack/%d/availability", t.Prefix, addr)
}
func (t Topics) PackState(addr byte) string { return fmt.Sprintf("%s/pack/%d/state", t.Prefix, addr) }
func (t Topics) SystemState() string        { return t.Prefix + "/system/state" }

func (t Topics) PackDeviceID(addr byte) string { return fmt.Sprintf("%s_pack_%d", t.SystemID, addr) }
func (t Topics) SystemDeviceID() string        { return t.SystemID + "_system" }

func (t Topics) PackDiscovery(addr byte) string {
	return fmt.Sprintf("%s/device/%s/config", t.DiscoveryPrefix, t.PackDeviceID(addr))
}
func (t Topics) SystemDiscovery() string {
	return fmt.Sprintf("%s/device/%s/config", t.DiscoveryPrefix, t.SystemDeviceID())
}

// Availability payloads.
const (
	Online  = "online"
	Offline = "offline"
)

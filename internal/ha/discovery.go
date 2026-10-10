package ha

import (
	"fmt"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
)

// Origin identifies the discovery source ("o" object).
type Origin struct {
	Name string `json:"name"`
	SW   string `json:"sw,omitempty"`
	URL  string `json:"url,omitempty"`
}

// DiscoveryOptions parametrise the payloads.
type DiscoveryOptions struct {
	Topics       Topics
	DeviceName   string // base name, e.g. "Felicity Battery" -> "Felicity Battery 1", "Felicity Battery System"
	Manufacturer string
	Model        string
	Origin       Origin
}

// Payload is a device discovery document (homeassistant/device/<id>/config).
type Payload map[string]any

// component is one entry of "cmps". Keys use the HA abbreviations.
type component map[string]any

func (c component) diagnostic() component { c["ent_cat"] = "diagnostic"; return c }
func (c component) disabled() component   { c["en"] = false; return c }
func (c component) icon(i string) component {
	c["ic"] = i
	return c
}

// attrs attaches a JSON attributes template reading from the shared state topic.
func (c component) attrs(stateTopic, tpl string) component {
	c["json_attr_t"] = stateTopic
	c["json_attr_tpl"] = tpl
	return c
}

func sensor(uniq, name, devClass, stateClass, unit string, precision int, tpl string) component {
	c := component{
		"p":       "sensor",
		"uniq_id": uniq,
		"name":    name,
		"val_tpl": tpl,
	}
	if devClass != "" {
		c["dev_cla"] = devClass
	}
	if stateClass != "" {
		c["stat_cla"] = stateClass
	}
	if unit != "" {
		c["unit_of_meas"] = unit
	}
	if precision >= 0 {
		c["sug_dsp_prc"] = precision
	}
	return c
}

func binary(uniq, name, devClass, boolExpr string) component {
	c := component{
		"p":       "binary_sensor",
		"uniq_id": uniq,
		"name":    name,
		"val_tpl": fmt.Sprintf("{{ 'ON' if %s else 'OFF' }}", boolExpr),
	}
	if devClass != "" {
		c["dev_cla"] = devClass
	}
	return c
}

func v(key string) string { return "{{ value_json." + key + " }}" }

// PackDiscovery builds the device discovery payload for one pack. The pack must be Complete().
func PackDiscovery(o DiscoveryOptions, p *felicity.Pack) Payload {
	t := o.Topics
	id := t.PackDeviceID(p.Address)
	uid := func(key string) string { return id + "_" + key }
	state := t.PackState(p.Address)

	dev := map[string]any{
		"ids":        []string{id},
		"name":       fmt.Sprintf("%s %d", o.DeviceName, p.Address),
		"mf":         o.Manufacturer,
		"mdl":        o.Model,
		"via_device": t.SystemDeviceID(),
	}
	if fw := felicity.VersionString(p.FirmwareVersion); fw != "" {
		dev["sw"] = fw
	}
	if hw := felicity.VersionString(p.HardwareVersion); hw != "" {
		dev["hw"] = hw
	}

	cmps := map[string]component{}
	add := func(key string, c component) { cmps[uid(key)] = c }

	// Primary sensors.
	add("voltage", sensor(uid("voltage"), "Voltage", "voltage", "measurement", "V", 2, v("voltage")))
	add("current", sensor(uid("current"), "Current", "current", "measurement", "A", 1, v("current")))
	add("power", sensor(uid("power"), "Power", "power", "measurement", "W", 0, v("power")))
	add("soc", sensor(uid("soc"), "State of charge", "battery", "measurement", "%", 0, v("soc")).
		attrs(state, "{{ {'address': value_json.address, 'cell_count': value_json.cell_count, "+
			"'temp_sensor_count': value_json.temp_sensor_count, 'cell_scale': value_json.cell_scale, "+
			"'voltage_scale': value_json.voltage_scale, 'last_update': value_json.last_update} | tojson }}"))
	add("temperature", sensor(uid("temperature"), "Temperature", "temperature", "measurement", "°C", 1, v("temperature")))
	add("remaining_ah", sensor(uid("remaining_ah"), "Remaining capacity", "", "measurement", "Ah", 1, v("remaining_ah")).
		icon("mdi:battery-medium"))
	add("cell_delta_mv", sensor(uid("cell_delta_mv"), "Cell voltage delta", "voltage", "measurement", "mV", 0, v("cell_delta_mv")).
		attrs(state, "{{ {'cell_min_index': value_json.cell_min_index, 'cell_max_index': value_json.cell_max_index} | tojson }}"))
	add("charging", binary(uid("charging"), "Charging", "battery_charging", "value_json.charging"))

	// Diagnostic, enabled.
	add("capacity_ah", sensor(uid("capacity_ah"), "Full charge capacity", "", "measurement", "Ah", 1, v("capacity_ah")).
		icon("mdi:battery-high").diagnostic())
	add("cell_min", sensor(uid("cell_min"), "Cell voltage min", "voltage", "measurement", "V", 3, v("cell_min")).
		attrs(state, "{{ {'cell': value_json.cell_min_index} | tojson }}").diagnostic())
	add("cell_max", sensor(uid("cell_max"), "Cell voltage max", "voltage", "measurement", "V", 3, v("cell_max")).
		attrs(state, "{{ {'cell': value_json.cell_max_index} | tojson }}").diagnostic())
	add("cell_avg", sensor(uid("cell_avg"), "Cell voltage average", "voltage", "measurement", "V", 3, v("cell_avg")).diagnostic())
	add("charge_voltage_limit", sensor(uid("charge_voltage_limit"), "Charge voltage limit", "voltage", "measurement", "V", 1, v("charge_voltage_limit")).diagnostic())
	add("discharge_voltage_limit", sensor(uid("discharge_voltage_limit"), "Discharge voltage limit", "voltage", "measurement", "V", 1, v("discharge_voltage_limit")).diagnostic())
	add("charge_current_limit", sensor(uid("charge_current_limit"), "Charge current limit", "current", "measurement", "A", 1, v("charge_current_limit")).diagnostic())
	add("discharge_current_limit", sensor(uid("discharge_current_limit"), "Discharge current limit", "current", "measurement", "A", 1, v("discharge_current_limit")).diagnostic())
	add("bus_errors", sensor(uid("bus_errors"), "Bus errors", "", "total_increasing", "", 0, v("bus_errors")).
		icon("mdi:alert-circle-outline").diagnostic())
	add("charge_enabled", binary(uid("charge_enabled"), "Charge enabled", "", "value_json.charge_enabled").
		icon("mdi:battery-arrow-up").diagnostic())
	add("discharge_enabled", binary(uid("discharge_enabled"), "Discharge enabled", "", "value_json.discharge_enabled").
		icon("mdi:battery-arrow-down").diagnostic())
	add("charge_immediately", binary(uid("charge_immediately"), "Charge immediately request", "", "value_json.charge_immediately").
		icon("mdi:battery-alert").diagnostic())
	add("full_charge_request", binary(uid("full_charge_request"), "Full charge request", "", "value_json.full_charge_request").
		icon("mdi:battery-charging-100").diagnostic())
	add("small_current_charge_request", binary(uid("small_current_charge_request"), "Small current charge request", "", "value_json.small_current_charge_request").
		icon("mdi:battery-charging-low").diagnostic())

	// Diagnostic, disabled by default.
	cells := int(p.CellCount)
	if cells > felicity.MaxCells {
		cells = felicity.MaxCells
	}
	for i := 1; i <= cells; i++ {
		key := fmt.Sprintf("cell_%d", i)
		add(key, sensor(uid(key), fmt.Sprintf("Cell %d voltage", i), "voltage", "measurement", "V", 3,
			fmt.Sprintf("{{ value_json.cells[%d] }}", i-1)).diagnostic().disabled())
	}
	temps := int(p.TempSensorCount)
	if temps > felicity.MaxTempSensors {
		temps = felicity.MaxTempSensors
	}
	for i := 1; i <= temps; i++ {
		key := fmt.Sprintf("temp_%d", i)
		name := fmt.Sprintf("Temperature sensor %d", i)
		if i == felicity.MOSFETSensor {
			name = "MOSFET temperature"
		}
		add(key, sensor(uid(key), name, "temperature", "measurement", "°C", 1,
			fmt.Sprintf("{{ value_json.temps[%d] }}", i-1)).diagnostic().disabled())
	}
	add("charge_current", sensor(uid("charge_current"), "Charge current", "current", "measurement", "A", 1, v("charge_current")).diagnostic().disabled())
	add("discharge_current", sensor(uid("discharge_current"), "Discharge current", "current", "measurement", "A", 1, v("discharge_current")).diagnostic().disabled())
	add("status_raw", sensor(uid("status_raw"), "BMS status register", "", "", "", -1, "{{ '0x%02x' % value_json.status_raw }}").
		attrs(state, "{{ {'flags': value_json.status_flags} | tojson }}").icon("mdi:hexadecimal").diagnostic().disabled())

	// Alarm / protection registers: only when the pack implements them.
	if p.HasAlarms {
		alarm := func(key, name, prefix string) {
			add(key, binary(uid(key), name, "problem", "value_json.alarms."+key).
				attrs(state, fmt.Sprintf("{{ {'flags': value_json.alarms.flags | select('match', '%s') | list} | tojson }}", prefix)).
				diagnostic())
		}
		alarm("charge_alarm", "Charge alarm", "charge_alarm:")
		alarm("discharge_alarm", "Discharge alarm", "discharge_alarm:")
		alarm("charge_protection", "Charge protection", "charge_protect")
		alarm("discharge_protection", "Discharge protection", "discharge_protect")
	}

	return Payload{
		"dev":       dev,
		"o":         o.Origin,
		"cmps":      cmps,
		"stat_t":    state,
		"avty":      []map[string]string{{"t": t.Availability()}, {"t": t.PackAvailability(p.Address)}},
		"avty_mode": "all",
		"qos":       0,
	}
}

// SystemDiscovery builds the device discovery payload for the aggregate device.
func SystemDiscovery(o DiscoveryOptions, packCount int, alarmsSupported bool) Payload {
	t := o.Topics
	id := t.SystemDeviceID()
	uid := func(key string) string { return id + "_" + key }
	state := t.SystemState()

	dev := map[string]any{
		"ids":  []string{id},
		"name": o.DeviceName + " System",
		"mf":   o.Manufacturer,
		"mdl":  fmt.Sprintf("%d× %s", packCount, o.Model),
	}

	cmps := map[string]component{}
	add := func(key string, c component) { cmps[uid(key)] = c }

	add("power", sensor(uid("power"), "Power", "power", "measurement", "W", 0, v("power")))
	add("current", sensor(uid("current"), "Current", "current", "measurement", "A", 1, v("current")))
	add("voltage", sensor(uid("voltage"), "Voltage", "voltage", "measurement", "V", 2, v("voltage")))
	add("soc", sensor(uid("soc"), "State of charge", "battery", "measurement", "%", 0, v("soc")).
		attrs(state, "{{ {'packs_online': value_json.packs_online, 'packs_configured': value_json.packs_configured, 'last_update': value_json.last_update} | tojson }}"))
	add("temperature", sensor(uid("temperature"), "Temperature", "temperature", "measurement", "°C", 1, v("temperature")))
	add("temperature_max", sensor(uid("temperature_max"), "Temperature max", "temperature", "measurement", "°C", 1, v("temperature_max")).
		attrs(state, "{{ {'pack': value_json.temperature_max_pack, 'sensor': value_json.temperature_max_sensor} | tojson }}"))
	add("stored_energy", sensor(uid("stored_energy"), "Stored energy", "energy_storage", "measurement", "kWh", 2, v("stored_kwh")))
	add("remaining_ah", sensor(uid("remaining_ah"), "Remaining capacity", "", "measurement", "Ah", 1, v("remaining_ah")).icon("mdi:battery-medium"))
	add("capacity_ah", sensor(uid("capacity_ah"), "Full charge capacity", "", "measurement", "Ah", 1, v("capacity_ah")).icon("mdi:battery-high"))
	add("charge_current_limit", sensor(uid("charge_current_limit"), "Charge current limit", "current", "measurement", "A", 1, v("charge_current_limit")))
	add("discharge_current_limit", sensor(uid("discharge_current_limit"), "Discharge current limit", "current", "measurement", "A", 1, v("discharge_current_limit")))
	add("charge_voltage_limit", sensor(uid("charge_voltage_limit"), "Charge voltage limit", "voltage", "measurement", "V", 1, v("charge_voltage_limit")))
	add("discharge_voltage_limit", sensor(uid("discharge_voltage_limit"), "Discharge voltage limit", "voltage", "measurement", "V", 1, v("discharge_voltage_limit")))
	add("charging", binary(uid("charging"), "Charging", "battery_charging", "value_json.charging"))

	add("packs_online", sensor(uid("packs_online"), "Packs online", "", "measurement", "", 0, v("packs_online_count")).
		attrs(state, "{{ {'online': value_json.packs_online, 'configured': value_json.packs_configured} | tojson }}").
		icon("mdi:battery-sync").diagnostic())
	add("cell_min", sensor(uid("cell_min"), "Cell voltage min", "voltage", "measurement", "V", 3, v("cell_min")).
		attrs(state, "{{ {'pack': value_json.cell_min_pack, 'cell': value_json.cell_min_index} | tojson }}").diagnostic())
	add("cell_max", sensor(uid("cell_max"), "Cell voltage max", "voltage", "measurement", "V", 3, v("cell_max")).
		attrs(state, "{{ {'pack': value_json.cell_max_pack, 'cell': value_json.cell_max_index} | tojson }}").diagnostic())
	add("cell_delta_mv", sensor(uid("cell_delta_mv"), "Cell voltage delta", "voltage", "measurement", "mV", 0, v("cell_delta_mv")).diagnostic())
	add("charge_enabled", binary(uid("charge_enabled"), "Charge enabled", "", "value_json.charge_enabled").icon("mdi:battery-arrow-up").diagnostic())
	add("discharge_enabled", binary(uid("discharge_enabled"), "Discharge enabled", "", "value_json.discharge_enabled").icon("mdi:battery-arrow-down").diagnostic())
	add("charge_immediately", binary(uid("charge_immediately"), "Charge immediately request", "", "value_json.charge_immediately").icon("mdi:battery-alert").diagnostic())
	add("problem", binary(uid("problem"), "Problem", "problem", "value_json.problem").
		attrs(state, "{{ {'alarm_flags': value_json.alarm_flags, 'packs_online': value_json.packs_online, 'packs_configured': value_json.packs_configured} | tojson }}").diagnostic())
	add("bus_errors", sensor(uid("bus_errors"), "Bus errors", "", "total_increasing", "", 0, v("bus_errors")).icon("mdi:alert-circle-outline").diagnostic())
	_ = alarmsSupported // the Problem sensor also covers offline packs, so it is always published

	return Payload{
		"dev":       dev,
		"o":         o.Origin,
		"cmps":      cmps,
		"stat_t":    state,
		"avty":      []map[string]string{{"t": t.Availability()}},
		"avty_mode": "all",
		"qos":       0,
	}
}

package ha

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
)

var update = flag.Bool("update", false, "rewrite golden files")

func testPack(addr byte, alarms bool) *felicity.Pack {
	p := felicity.NewPack(addr)
	p.Online = true
	cells := []uint16{16, 3342, 3342, 3343, 3342, 3342, 3342, 3343, 3342, 3342, 3342, 3343, 3343, 3343, 3342, 3342, 3340, 0, 0, 0, 0}
	p.Apply(16, cells)
	p.Apply(37, []uint16{4, 3012, 3012, 3012, 3002, 0, 0, 0, 0, 0, 0, 0, 19, 53410, 95, 5, 22320})
	p.Apply(112, []uint16{576, 480, 320, 1600, 0xC0})
	if alarms {
		p.Apply(54, []uint16{3, 0, 0, 0, 0, 0, 0})
		p.Apply(1, []uint16{0, 0, 1, 2, 0, 0})
	}
	return p
}

func opts() DiscoveryOptions {
	return DiscoveryOptions{
		Topics:       Topics{Prefix: "felicity", DiscoveryPrefix: "homeassistant", SystemID: "felicity"},
		DeviceName:   "Felicity Battery",
		Manufacturer: "Felicity Solar",
		Model:        "FLB48314TG1",
		Origin:       Origin{Name: "felicityd", SW: "test", URL: "https://example.invalid"},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from golden file; run `go test ./internal/ha -update` if the change is intended", name)
	}
}

func pretty(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

var tplKey = regexp.MustCompile(`value_json\.([a-z_0-9]+)(?:\.([a-z_0-9]+))?`)

// checkTemplates verifies that every value_json reference in a payload resolves to a key of the
// state document, and that unique_ids are unique.
func checkTemplates(t *testing.T, payload Payload, state []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(state, &doc); err != nil {
		t.Fatal(err)
	}
	cmps := payload["cmps"].(map[string]component)
	seen := map[string]bool{}
	for key, c := range cmps {
		uid := c["uniq_id"].(string)
		if seen[uid] || key != uid {
			t.Errorf("duplicate or mismatched unique_id %q (component key %q)", uid, key)
		}
		seen[uid] = true
		if c["p"] == "binary_sensor" && c["dev_cla"] == nil && c["ic"] == nil {
			t.Errorf("%s: binary sensor without device class or icon", uid)
		}
		for _, field := range []string{"val_tpl", "json_attr_tpl"} {
			tpl, ok := c[field].(string)
			if !ok {
				continue
			}
			for _, m := range tplKey.FindAllStringSubmatch(tpl, -1) {
				val, ok := doc[m[1]]
				if !ok {
					t.Errorf("%s: %s references missing state key %q", uid, field, m[1])
					continue
				}
				if m[2] != "" {
					sub, ok := val.(map[string]any)
					if !ok {
						t.Errorf("%s: %s dereferences non-object key %q", uid, field, m[1])
					} else if _, ok := sub[m[2]]; !ok {
						t.Errorf("%s: %s references missing state key %q.%q", uid, field, m[1], m[2])
					}
				}
			}
		}
	}
}

func TestPackDiscoveryGolden(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		alarms bool
	}{{"pack_basic", false}, {"pack_alarms", true}} {
		p := testPack(1, tc.alarms)
		payload := PackDiscovery(opts(), p)
		state := Marshal(BuildPackState(p, now))
		checkTemplates(t, payload, state)
		golden(t, tc.name+"_discovery.json", pretty(t, payload))
		var st any
		_ = json.Unmarshal(state, &st)
		golden(t, tc.name+"_state.json", pretty(t, st))

		cmps := payload["cmps"].(map[string]component)
		if _, ok := cmps["felicity_pack_1_cell_16"]; !ok {
			t.Error("missing cell 16")
		}
		if _, ok := cmps["felicity_pack_1_cell_17"]; ok {
			t.Error("cell 17 should not exist for a 16S pack")
		}
		if c := cmps["felicity_pack_1_cell_7"]; c["en"] != false || c["ent_cat"] != "diagnostic" {
			t.Errorf("cell sensors must be diagnostic and disabled: %v", c)
		}
		if _, ok := cmps["felicity_pack_1_charge_alarm"]; ok != tc.alarms {
			t.Errorf("alarm components present=%v, want %v", ok, tc.alarms)
		}
		dev := payload["dev"].(map[string]any)
		if _, ok := dev["sw"]; ok != tc.alarms {
			t.Errorf("sw version present=%v, want %v", ok, tc.alarms)
		}
		if !strings.Contains(string(state), `"voltage":53.41`) || !strings.Contains(string(state), `"temperature":28.1`) {
			t.Errorf("state: %s", state)
		}
	}
}

func TestSystemDiscoveryGolden(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	packs := []*felicity.Pack{testPack(1, false), testPack(2, false), testPack(3, false)}
	sys := felicity.Aggregate(packs, 51.2)
	payload := SystemDiscovery(opts(), len(packs), sys.AlarmsSupported)
	state := Marshal(BuildSystemState(sys, now))
	checkTemplates(t, payload, state)
	golden(t, "system_discovery.json", pretty(t, payload))
	var st any
	_ = json.Unmarshal(state, &st)
	golden(t, "system_state.json", pretty(t, st))
	if payload["dev"].(map[string]any)["mdl"] != "3× FLB48314TG1" {
		t.Error("model")
	}
}

func TestTopics(t *testing.T) {
	tp := Topics{Prefix: "felicity", DiscoveryPrefix: "homeassistant", SystemID: "sys"}
	if tp.PackDiscovery(2) != "homeassistant/device/sys_pack_2/config" || tp.SystemState() != "felicity/system/state" ||
		tp.PackAvailability(3) != "felicity/pack/3/availability" {
		t.Error("topics")
	}
}

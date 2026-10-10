package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[strings.TrimPrefix(k, "FELICITYD_")] }
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.SerialPort != "/dev/ttyUSB0" || c.MQTTPort != 1883 || c.StatusInterval != 5*time.Second || !c.HADiscovery ||
		c.TopicPrefix != "felicity" || c.NominalVoltage != 51.2 || c.LogLevel != slog.LevelInfo || len(c.PackAddresses) != 0 {
		t.Errorf("%+v", c)
	}
	if err := c.RequireMQTT(); err == nil {
		t.Error("MQTT host should be required")
	}
}

func TestOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"MQTT_HOST": "broker", "MQTT_TLS": "true", "PACK_ADDRESSES": "1, 2,3", "STATUS_INTERVAL": "2s",
		"LOG_LEVEL": "debug", "MQTT_TOPIC_PREFIX": "/home/bat/", "ACCEPT_TRUNCATED_CRC": "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.MQTTPort != 8883 || len(c.PackAddresses) != 3 || c.PackAddresses[2] != 3 || c.StatusInterval != 2*time.Second ||
		c.LogLevel != slog.LevelDebug || c.TopicPrefix != "home/bat" || !c.AcceptTruncatedCRC {
		t.Errorf("%+v", c)
	}
	if err := c.RequireMQTT(); err != nil {
		t.Error(err)
	}
}

func TestErrors(t *testing.T) {
	_, err := Load(env(map[string]string{"STATUS_INTERVAL": "soon", "PACK_ADDRESSES": "0,x", "LOG_LEVEL": "loud", "SYSTEM_ID": "a/b"}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"STATUS_INTERVAL", "PACK_ADDRESSES", "LOG_LEVEL", "SYSTEM_ID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

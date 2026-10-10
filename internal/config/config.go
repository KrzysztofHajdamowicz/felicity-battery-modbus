// Package config loads felicityd settings from FELICITYD_* environment variables
// (systemd: EnvironmentFile=/etc/default/felicityd).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the complete daemon configuration.
type Config struct {
	SerialPort    string
	PackAddresses []byte // empty = enumerate
	MaxAddress    byte

	StatusInterval  time.Duration
	CellsInterval   time.Duration
	LimitsInterval  time.Duration
	AlarmsInterval  time.Duration
	OfflineInterval time.Duration
	BusGap          time.Duration
	BusTimeout      time.Duration
	OfflineAfter    int

	AcceptTruncatedCRC bool

	MQTTHost        string
	MQTTPort        int
	MQTTTLS         bool
	MQTTTLSInsecure bool
	MQTTCAFile      string
	MQTTUsername    string
	MQTTPassword    string
	MQTTClientID    string
	TopicPrefix     string

	HADiscovery       bool
	HADiscoveryPrefix string

	SystemID       string
	DeviceName     string
	Manufacturer   string
	PackModel      string
	NominalVoltage float64

	LogLevel slog.Level
}

// Load reads the configuration from getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	var errs []string
	str := func(key, def string) string {
		if v := strings.TrimSpace(getenv("FELICITYD_" + key)); v != "" {
			return v
		}
		return def
	}
	dur := func(key string, def time.Duration) time.Duration {
		v := str(key, "")
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("FELICITYD_%s: invalid duration %q", key, v))
			return def
		}
		return d
	}
	boolean := func(key string, def bool) bool {
		v := str(key, "")
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("FELICITYD_%s: invalid boolean %q", key, v))
			return def
		}
		return b
	}
	integer := func(key string, def, min, max int) int {
		v := str(key, "")
		if v == "" {
			return def
		}
		i, err := strconv.Atoi(v)
		if err != nil || i < min || i > max {
			errs = append(errs, fmt.Sprintf("FELICITYD_%s: expected integer in %d..%d, got %q", key, min, max, v))
			return def
		}
		return i
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	c := Config{
		SerialPort:      str("SERIAL_PORT", "/dev/ttyUSB0"),
		MaxAddress:      byte(integer("MAX_ADDRESS", 16, 1, 247)),
		StatusInterval:  dur("STATUS_INTERVAL", 5*time.Second),
		CellsInterval:   dur("CELLS_INTERVAL", 30*time.Second),
		LimitsInterval:  dur("LIMITS_INTERVAL", 60*time.Second),
		AlarmsInterval:  dur("ALARMS_INTERVAL", 30*time.Second),
		OfflineInterval: dur("OFFLINE_INTERVAL", 30*time.Second),
		BusGap:          dur("BUS_GAP", 200*time.Millisecond),
		BusTimeout:      dur("BUS_TIMEOUT", 500*time.Millisecond),
		OfflineAfter:    integer("OFFLINE_AFTER", 5, 1, 1000),

		AcceptTruncatedCRC: boolean("ACCEPT_TRUNCATED_CRC", false),

		MQTTHost:        str("MQTT_HOST", ""),
		MQTTTLS:         boolean("MQTT_TLS", false),
		MQTTTLSInsecure: boolean("MQTT_TLS_INSECURE", false),
		MQTTCAFile:      str("MQTT_CA_FILE", ""),
		MQTTUsername:    str("MQTT_USERNAME", ""),
		MQTTPassword:    str("MQTT_PASSWORD", ""),
		MQTTClientID:    str("MQTT_CLIENT_ID", "felicityd-"+hostname),
		TopicPrefix:     strings.Trim(str("MQTT_TOPIC_PREFIX", "felicity"), "/"),

		HADiscovery:       boolean("HA_DISCOVERY", true),
		HADiscoveryPrefix: strings.Trim(str("HA_DISCOVERY_PREFIX", "homeassistant"), "/"),

		SystemID:     str("SYSTEM_ID", "felicity"),
		DeviceName:   str("DEVICE_NAME", "Felicity Battery"),
		Manufacturer: str("MANUFACTURER", "Felicity Solar"),
		PackModel:    str("PACK_MODEL", "FLB48314TG1"),
	}
	defPort := 1883
	if c.MQTTTLS {
		defPort = 8883
	}
	c.MQTTPort = integer("MQTT_PORT", defPort, 1, 65535)

	if v := str("NOMINAL_VOLTAGE", "51.2"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			errs = append(errs, fmt.Sprintf("FELICITYD_NOMINAL_VOLTAGE: invalid number %q", v))
		}
		c.NominalVoltage = f
	}

	if v := str("PACK_ADDRESSES", ""); v != "" {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			a, err := strconv.Atoi(part)
			if err != nil || a < 1 || a > 247 {
				errs = append(errs, fmt.Sprintf("FELICITYD_PACK_ADDRESSES: invalid address %q", part))
				continue
			}
			c.PackAddresses = append(c.PackAddresses, byte(a))
		}
	}

	switch strings.ToLower(str("LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		errs = append(errs, "FELICITYD_LOG_LEVEL: expected debug|info|warn|error")
	}

	for _, bad := range []string{"+", "#"} {
		if strings.Contains(c.TopicPrefix, bad) || strings.Contains(c.SystemID, bad) || strings.Contains(c.SystemID, "/") {
			errs = append(errs, "FELICITYD_MQTT_TOPIC_PREFIX / FELICITYD_SYSTEM_ID must not contain MQTT wildcards or '/'")
			break
		}
	}

	if len(errs) > 0 {
		return c, fmt.Errorf("configuration errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return c, nil
}

// RequireMQTT validates the settings needed to connect to a broker.
func (c Config) RequireMQTT() error {
	if c.MQTTHost == "" {
		return fmt.Errorf("FELICITYD_MQTT_HOST is required")
	}
	return nil
}

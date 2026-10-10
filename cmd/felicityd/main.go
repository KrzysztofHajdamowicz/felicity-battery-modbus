// felicityd polls Felicity Solar LiFePO4 battery packs over RS485 and publishes them to MQTT with
// Home Assistant discovery. Configuration comes from FELICITYD_* environment variables.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/bus"
	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/config"
	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/felicity"
	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/ha"
	"github.com/KrzysztofHajdamowicz/felicity-battery-modbus/internal/mqtt"
)

// Set with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const repoURL = "https://github.com/KrzysztofHajdamowicz/felicity-battery-modbus"

func main() {
	scan := flag.Bool("scan", false, "enumerate the packs, print a summary and exit")
	dryRun := flag.Bool("dry-run", false, "log MQTT publishes instead of connecting to a broker")
	replay := flag.String("replay", "", "answer bus requests from a capture file instead of the serial port")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("felicityd", version)
		return
	}
	if err := run(*scan, *dryRun, *replay); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("felicityd exiting", "err", err)
		os.Exit(1)
	}
}

func run(scan, dryRun bool, replay string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("felicityd starting", "version", version)
	bus.AcceptTruncatedCRC = cfg.AcceptTruncatedCRC

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var transport bus.Transport
	if replay != "" {
		transport, err = bus.OpenReplay(replay)
		log.Info("replaying capture", "file", replay)
	} else {
		transport, err = bus.OpenSerial(cfg.SerialPort, cfg.BusTimeout)
		log.Info("serial port open", "port", cfg.SerialPort)
	}
	if err != nil {
		return err
	}
	defer func() { _ = transport.Close() }()

	gap := cfg.BusGap
	if replay != "" {
		gap = time.Millisecond
	}
	packs, err := bus.Scan(ctx, transport, bus.ScanOptions{
		Addresses:  cfg.PackAddresses,
		MaxAddress: cfg.MaxAddress,
		Gap:        maxDur(gap, 500*time.Millisecond, replay == ""),
		Log:        log,
	})
	if err != nil {
		return err
	}
	if scan {
		printScan(packs)
		return nil
	}

	topics := ha.Topics{Prefix: cfg.TopicPrefix, DiscoveryPrefix: cfg.HADiscoveryPrefix, SystemID: cfg.SystemID}
	pub := &publisher{
		cfg:    cfg,
		topics: topics,
		packs:  packs,
		log:    log,
		disc: ha.DiscoveryOptions{
			Topics:       topics,
			DeviceName:   cfg.DeviceName,
			Manufacturer: cfg.Manufacturer,
			Model:        cfg.PackModel,
			Origin:       ha.Origin{Name: "felicityd", SW: version, URL: repoURL},
		},
	}

	if dryRun {
		pub.out = mqtt.LogPublisher{Log: log}
		pub.publishAll()
	} else {
		if err := cfg.RequireMQTT(); err != nil {
			return err
		}
		client, err := mqtt.Connect(ctx, mqtt.Options{
			Host: cfg.MQTTHost, Port: cfg.MQTTPort, TLS: cfg.MQTTTLS, TLSInsecure: cfg.MQTTTLSInsecure,
			CAFile: cfg.MQTTCAFile, Username: cfg.MQTTUsername, Password: cfg.MQTTPassword,
			ClientID: cfg.MQTTClientID, WillTopic: topics.Availability(), WillPayload: ha.Offline,
			OnConnect: pub.publishAll, Log: log,
		})
		if err != nil {
			return err
		}
		pub.setOutput(client)
		pub.publishAll()
		defer func() {
			pub.publish(topics.Availability(), []byte(ha.Offline))
			client.Disconnect()
			log.Info("mqtt disconnected")
		}()
	}

	poller := &bus.Poller{
		Transport: transport,
		Packs:     packs,
		Intervals: bus.Intervals{
			Status: cfg.StatusInterval, Cells: cfg.CellsInterval, Limits: cfg.LimitsInterval,
			Alarms: cfg.AlarmsInterval, Offline: cfg.OfflineInterval,
		},
		Gap:          gap,
		OfflineAfter: cfg.OfflineAfter,
		OnUpdate:     pub.onUpdate,
		Log:          log,
	}
	log.Info("polling", "packs", len(packs), "status_interval", cfg.StatusInterval, "cells_interval", cfg.CellsInterval, "limits_interval", cfg.LimitsInterval)
	err = poller.Run(ctx)
	if errors.Is(err, context.Canceled) {
		log.Info("shutting down")
		return nil
	}
	return err
}

func maxDur(gap, floor time.Duration, apply bool) time.Duration {
	if apply && gap < floor {
		return floor
	}
	return gap
}

// publisher serialises the pack state into MQTT messages. It is called from the poller goroutine
// and from paho's reconnect handler, hence the mutex.
type publisher struct {
	mu     sync.Mutex
	cfg    config.Config
	topics ha.Topics
	packs  []*felicity.Pack
	disc   ha.DiscoveryOptions
	out    mqtt.Publisher
	log    *slog.Logger
}

func (p *publisher) setOutput(out mqtt.Publisher) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out = out
}

// publish sends one retained message. Everything felicityd publishes is retained so that a
// restarted Home Assistant immediately sees discovery, availability and the last state.
func (p *publisher) publish(topic string, payload []byte) {
	if p.out == nil {
		return
	}
	if err := p.out.Publish(topic, payload, true); err != nil {
		p.log.Warn("publish failed", "topic", topic, "err", err)
	}
}

// publishAll sends availability, discovery and the current state of everything. Used at start-up
// and after every reconnect so a restarted broker / Home Assistant sees the full picture.
func (p *publisher) publishAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out == nil {
		return
	}
	now := time.Now()
	p.publish(p.topics.Availability(), []byte(ha.Online))
	alarms := false
	for _, pk := range p.packs {
		alarms = alarms || pk.HasAlarms
	}
	if p.cfg.HADiscovery {
		p.publish(p.topics.SystemDiscovery(), ha.Marshal(ha.SystemDiscovery(p.disc, len(p.packs), alarms)))
	}
	for _, pk := range p.packs {
		if !pk.Complete() {
			p.publish(p.topics.PackAvailability(pk.Address), []byte(ha.Offline))
			continue
		}
		if p.cfg.HADiscovery {
			p.publish(p.topics.PackDiscovery(pk.Address), ha.Marshal(ha.PackDiscovery(p.disc, pk)))
		}
		p.publishPackAvailability(pk)
		p.publishPackState(pk, now)
	}
	p.publishSystem(now)
}

func (p *publisher) publishPackAvailability(pk *felicity.Pack) {
	avail := ha.Offline
	if pk.Online {
		avail = ha.Online
	}
	p.publish(p.topics.PackAvailability(pk.Address), []byte(avail))
}

func (p *publisher) publishPackState(pk *felicity.Pack, now time.Time) {
	p.publish(p.topics.PackState(pk.Address), ha.Marshal(ha.BuildPackState(pk, now)))
}

func (p *publisher) publishSystem(now time.Time) {
	sys := felicity.Aggregate(p.packs, p.cfg.NominalVoltage)
	p.publish(p.topics.SystemState(), ha.Marshal(ha.BuildSystemState(sys, now)))
}

func (p *publisher) onUpdate(pk *felicity.Pack, _ felicity.Block, onlineChanged bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if onlineChanged && pk.Online && p.cfg.HADiscovery {
		// A pack that came (back) online may be one we never published discovery for.
		p.publish(p.topics.PackDiscovery(pk.Address), ha.Marshal(ha.PackDiscovery(p.disc, pk)))
	}
	if onlineChanged {
		p.publishPackAvailability(pk)
	}
	if pk.Complete() {
		p.publishPackState(pk, now)
	}
	p.publishSystem(now)
}

func printScan(packs []*felicity.Pack) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADDR\tONLINE\tCELLS\tTEMPS\tVOLTAGE\tCURRENT\tPOWER\tSOC\tCAPACITY\tTEMP\tCELL MIN/MAX\tI LIMITS (CHG/DIS)\tSCALE (CELL/PACK)\tFW\tALARM REGS")
	for _, p := range packs {
		if !p.Complete() {
			fmt.Fprintf(w, "%d\t%v\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\n", p.Address, p.Online)
			continue
		}
		temp := "-"
		if t, ok := p.Temperature(); ok {
			temp = fmt.Sprintf("%.1f °C", t)
		}
		cells := "-"
		if cs, ok := p.CellStats(); ok {
			cells = fmt.Sprintf("%.3f/%.3f (Δ %.0f mV)", cs.Min, cs.Max, cs.DeltaMilliV)
		}
		fw := felicity.VersionString(p.FirmwareVersion)
		if fw == "" {
			fw = "-"
		}
		fmt.Fprintf(w, "%d\t%v\t%d\t%d\t%.2f V\t%.1f A\t%.0f W\t%d %%\t%.1f Ah\t%s\t%s\t%.0f/%.0f A\t%g/%g\t%s\t%v\n",
			p.Address, p.Online, p.CellCount, p.TempSensorCount, p.Voltage(), p.Current(), p.Power(), p.SOC,
			p.FullCapacityAh(), temp, cells, p.ChargeCurrentLimit(), p.DischargeCurrentLimit(),
			float64(p.CellScale), float64(p.VoltageScale), fw, p.HasAlarms)
	}
	_ = w.Flush()
	sys := felicity.Aggregate(packs, 51.2)
	fmt.Printf("\nSystem: %d/%d packs online, %.2f V, %.1f A, %.0f W, SOC %.1f %%, %.1f/%.1f Ah, charge limit %.0f A, discharge limit %.0f A, flags: %s\n",
		len(sys.PacksOnline), len(sys.PacksConfigured), sys.Voltage, sys.Current, sys.Power, sys.SOC, sys.RemainingAh, sys.FullCapacityAh,
		sys.ChargeCurrentLimit, sys.DischargeCurrentLimit, strings.Join(sys.AlarmFlags, ","))
}

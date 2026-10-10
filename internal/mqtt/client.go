// Package mqtt wraps paho.mqtt.golang with the handful of features felicityd needs: TLS or
// plaintext, username/password, a last-will availability message, retained publishes and a
// reconnect hook.
package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Publisher is what the rest of the daemon needs from a broker connection.
type Publisher interface {
	Publish(topic string, payload []byte, retain bool) error
}

// Options configures Connect.
type Options struct {
	Host        string
	Port        int
	TLS         bool
	TLSInsecure bool
	CAFile      string
	Username    string
	Password    string
	ClientID    string

	// WillTopic / WillPayload is published by the broker when the connection drops.
	WillTopic   string
	WillPayload string

	// OnConnect runs after every (re)connection, in paho's goroutine.
	OnConnect func()
	Log       *slog.Logger
}

// Client is a connected broker session.
type Client struct {
	c   paho.Client
	log *slog.Logger
}

const publishTimeout = 10 * time.Second

// Connect dials the broker and blocks until the first connection succeeds or ctx ends.
func Connect(ctx context.Context, o Options) (*Client, error) {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	scheme := "tcp"
	if o.TLS {
		scheme = "ssl"
	}
	opts := paho.NewClientOptions().
		AddBroker(fmt.Sprintf("%s://%s:%d", scheme, o.Host, o.Port)).
		SetClientID(o.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(time.Minute).
		SetKeepAlive(30 * time.Second).
		SetOrderMatters(false).
		SetCleanSession(true)
	if o.Username != "" {
		opts.SetUsername(o.Username).SetPassword(o.Password)
	}
	if o.WillTopic != "" {
		opts.SetWill(o.WillTopic, o.WillPayload, 1, true)
	}
	if o.TLS {
		cfg := &tls.Config{InsecureSkipVerify: o.TLSInsecure} //nolint:gosec // explicit opt-in
		if o.CAFile != "" {
			pem, err := os.ReadFile(o.CAFile)
			if err != nil {
				return nil, fmt.Errorf("read CA file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("no certificates found in %s", o.CAFile)
			}
			cfg.RootCAs = pool
		}
		opts.SetTLSConfig(cfg)
	}
	opts.SetOnConnectHandler(func(paho.Client) {
		log.Info("mqtt connected", "broker", o.Host, "port", o.Port)
		if o.OnConnect != nil {
			o.OnConnect()
		}
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Warn("mqtt connection lost", "err", err)
	})

	c := paho.NewClient(opts)
	tok := c.Connect()
	select {
	case <-tok.Done():
	case <-ctx.Done():
		c.Disconnect(0)
		return nil, ctx.Err()
	}
	if err := tok.Error(); err != nil {
		return nil, fmt.Errorf("mqtt connect: %w", err)
	}
	return &Client{c: c, log: log}, nil
}

// Publish sends one message at QoS 1 (retained ones) or QoS 0 (state updates).
func (c *Client) Publish(topic string, payload []byte, retain bool) error {
	qos := byte(0)
	if retain {
		qos = 1
	}
	tok := c.c.Publish(topic, qos, retain, payload)
	if !tok.WaitTimeout(publishTimeout) {
		return fmt.Errorf("publish %s: timeout", topic)
	}
	return tok.Error()
}

// Disconnect flushes and closes the connection.
func (c *Client) Disconnect() { c.c.Disconnect(500) }

// LogPublisher prints publishes instead of sending them (-dry-run).
type LogPublisher struct {
	Log *slog.Logger
}

// Publish logs the message.
func (l LogPublisher) Publish(topic string, payload []byte, retain bool) error {
	l.Log.Info("publish", "topic", topic, "retain", retain, "payload", string(payload))
	return nil
}

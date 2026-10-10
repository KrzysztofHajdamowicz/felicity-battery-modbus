BINARY   := felicityd
MODULE   := github.com/KrzysztofHajdamowicz/felicity-battery-modbus
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  := -trimpath
PI_HOST  ?= pi@raspberrypi.local
PI_ARCH  ?= arm64

.PHONY: all build test lint vet fmt build-linux build-arm64 build-amd64 build-armv7 scan replay deploy clean

all: build

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/felicityd

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w ./cmd ./internal

lint:
	golangci-lint run ./...

build-linux: build-arm64 build-amd64 build-armv7

build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_linux_arm64 ./cmd/felicityd

build-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_linux_amd64 ./cmd/felicityd

build-armv7:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_linux_armv7 ./cmd/felicityd

# Run against the recorded bus capture, no hardware or broker needed.
replay:
	go run ./cmd/felicityd -replay captures/2026-10-10-rs485-bus.log -dry-run

scan:
	go run ./cmd/felicityd -replay captures/2026-10-10-rs485-bus.log -scan

# Cross-compile for the Pi, copy the binary and the unit, (re)start the service.
# First time: also copy deploy/felicityd.env.example to /etc/default/felicityd on the Pi and edit it.
deploy: build-$(PI_ARCH)
	scp dist/$(BINARY)_linux_$(PI_ARCH) $(PI_HOST):/tmp/$(BINARY)
	scp deploy/felicityd.service $(PI_HOST):/tmp/felicityd.service
	ssh $(PI_HOST) 'sudo install -m 0755 /tmp/$(BINARY) /usr/local/bin/$(BINARY) \
		&& sudo install -m 0644 /tmp/felicityd.service /etc/systemd/system/felicityd.service \
		&& sudo test -f /etc/default/felicityd || echo "WARNING: /etc/default/felicityd missing, see deploy/felicityd.env.example"; \
		sudo systemctl daemon-reload && sudo systemctl enable --now felicityd && sudo systemctl restart felicityd \
		&& sleep 2 && systemctl --no-pager status felicityd | head -20'

clean:
	rm -rf bin dist

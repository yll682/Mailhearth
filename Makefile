SHELL := /bin/sh
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all web build run dev test lint docker clean

all: build

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o mailhearth ./cmd/mailhearth

# Run against the embedded fake Purelymail/IMAP/SMTP with demo data.
dev:
	MAILHEARTH_DEV_STACK=1 MAILHEARTH_DATA_DIR=./data/dev MAILHEARTH_LOG_LEVEL=debug go run ./cmd/mailhearth -seed-demo

test:
	go vet ./... && go test ./...
	cd web && npm run typecheck

docker:
	docker build --build-arg VERSION=$(VERSION) -t mailhearth:$(VERSION) .

clean:
	rm -rf mailhearth mailhearth.exe internal/web/dist/assets data/dev

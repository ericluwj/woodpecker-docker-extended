.PHONY: build test integration vet
VERSION ?= dev
COMMIT := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.buildVersion=$(VERSION) -X main.buildCommit=$(COMMIT)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/woodpecker-agent-docker-extended ./cmd/woodpecker-agent-docker-extended

test:
	go test -race ./...

vet:
	go vet ./...

integration:
	DOCKER_EXTENDED_INTEGRATION=1 go test -race -tags=integration -timeout=15m -v ./backend/dockerextended

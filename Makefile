SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

GO ?= go
GOBIN ?= $(CURDIR)/.bin

AGG_PORT ?= 9090
PROM_PORT ?= 9090
PROM1_EXT ?= 9091
PROM2_EXT ?= 9092
GO_VERSION ?= $(shell awk '/^go / {print $$2}' go.mod)

COMPOSE_ENV = AGG_PORT=$(AGG_PORT) PROM_PORT=$(PROM_PORT) PROM1_EXT=$(PROM1_EXT) PROM2_EXT=$(PROM2_EXT) GO_VERSION=$(GO_VERSION)

.PHONY: help tools-install tools-install-ls build test test-one test-race cover-html fmt vet tidy lint vulncheck check compose-config compose-up compose-down smoke

help:
	@printf "%s\n" \
	"make tools-install              # install staticcheck, revive, govulncheck into .bin/" \
	"make tools-install-ls           # install gopls into .bin/" \
	"make build                      # go build ./..." \
	"make test                       # go test ./..." \
	"make test-one TEST='TestX'      # run one aggregator test by regex/name" \
	"make test-race                  # go test with race + cover.out" \
	"make cover-html                 # write cover.html from cover.out" \
	"make fmt                        # go fmt ./..." \
	"make vet                        # go vet ./..." \
	"make tidy                       # go mod tidy" \
	"make lint                       # staticcheck + revive" \
	"make vulncheck                  # govulncheck ./..." \
	"make check                      # build + vet + test + lint" \
	"make compose-config             # validate docker-compose.yaml with CI-like env" \
	"make smoke                      # compose up + health probes + compose down"

tools-install:
	mkdir -p "$(GOBIN)"
	GOBIN="$(GOBIN)" $(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	GOBIN="$(GOBIN)" $(GO) install github.com/mgechev/revive@latest
	GOBIN="$(GOBIN)" $(GO) install golang.org/x/vuln/cmd/govulncheck@latest

tools-install-ls:
	mkdir -p "$(GOBIN)"
	GOBIN="$(GOBIN)" $(GO) install golang.org/x/tools/gopls@latest

build:
	$(GO) build ./...

test:
	$(GO) test ./...

test-one:
	@test -n "$(TEST)" || (echo "TEST is required, e.g. make test-one TEST='TestAddCustomLabel'" && exit 1)
	$(GO) test ./pkg/aggregator -run "$(TEST)" -v

test-race:
	$(GO) test -race -coverprofile=cover.out ./...

cover-html: test-race
	$(GO) tool cover -html=cover.out -o cover.html

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

lint: tools-install
	"$(GOBIN)/staticcheck" ./...
	"$(GOBIN)/revive" -formatter friendly ./...

vulncheck: tools-install
	"$(GOBIN)/govulncheck" ./...

check: build vet test lint

compose-config:
	$(COMPOSE_ENV) docker compose config

compose-up:
	$(COMPOSE_ENV) docker compose up -d --wait

compose-down:
	$(COMPOSE_ENV) docker compose down -v

smoke:
	trap '$(COMPOSE_ENV) docker compose down -v' EXIT; \
	$(COMPOSE_ENV) docker compose up -d --wait; \
	for i in $$(seq 1 10); do \
		if curl -fs "http://localhost:$(AGG_PORT)/metrics" >/dev/null; then \
			break; \
		fi; \
		sleep 2; \
	done; \
	curl -fs "http://localhost:$(AGG_PORT)/metrics" >/dev/null; \
	curl -fs "http://localhost:$(PROM1_EXT)/-/healthy" >/dev/null; \
	curl -fs "http://localhost:$(PROM2_EXT)/-/healthy" >/dev/null

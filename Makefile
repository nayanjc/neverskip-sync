SHELL := /bin/bash

BINARY  ?= neverskip-sync
BIN_DIR ?= bin

.PHONY: build build-linux test vet tidy run-once token-env clean help

help:
	@echo "targets:"
	@echo "  build         compile for the host"
	@echo "  build-linux   cross-compile static binary for linux/amd64 (deploy target)"
	@echo "  test          run all unit tests"
	@echo "  vet           go vet"
	@echo "  tidy          go mod tidy"
	@echo "  run-once      run a single tick locally (requires .env with NEVERSKIP_TOKEN + NTFY_TOPIC)"
	@echo "  token-env     print NEVERSKIP_TOKEN=... extracted from local Chrome cookies"
	@echo "  clean         remove $(BIN_DIR)"

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/server

build-linux:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY).linux-amd64 ./cmd/server

test:
	go test ./... -count=1

vet:
	go vet ./...

tidy:
	go mod tidy

run-once: build
	@if [ ! -f .env ]; then echo "missing .env — see README §local-dev"; exit 1; fi
	@set -a; source .env; set +a; \
		SQLITE_PATH=$${SQLITE_PATH:-./state.db} \
		$(BIN_DIR)/$(BINARY) -once -no-server

token-env:
	@.venv/bin/python scripts/extract_token.py --env

clean:
	rm -rf $(BIN_DIR)

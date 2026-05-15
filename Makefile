SHELL := /bin/bash

BINARY  ?= neverskip-sync
BIN_DIR ?= bin

.PHONY: build build-linux build-refresh-token build-refresh-token-linux test vet tidy run-once token-env refresh-token clean help

help:
	@echo "targets:"
	@echo "  build                    compile the server for the host"
	@echo "  build-linux              cross-compile the server static for linux/amd64"
	@echo "  build-refresh-token      compile the refresh-token CLI for the host"
	@echo "  build-refresh-token-linux  cross-compile refresh-token static for linux/amd64"
	@echo "  test                     run all unit tests"
	@echo "  vet                      go vet"
	@echo "  tidy                     go mod tidy"
	@echo "  run-once                 run one poll tick locally (requires .env)"
	@echo "  token-env                print NEVERSKIP_TOKEN=... extracted from local Chrome"
	@echo "  refresh-token            interactive: log in with mobile+password+captcha,"
	@echo "                           write the new token to \$$TOKEN_FILE (or stdout)"
	@echo "  clean                    remove $(BIN_DIR)"

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/server

build-linux:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY).linux-amd64 ./cmd/server

build-refresh-token:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/refresh-token ./cmd/refresh-token

build-refresh-token-linux:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/refresh-token.linux-amd64 ./cmd/refresh-token

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

# Interactive re-pair flow. Reads $TOKEN_FILE from your environment (or .env)
# and writes the freshly-issued token there atomically. The running service
# picks up the new value within ~5 seconds via the file-watching token
# provider — no restart needed.
refresh-token: build-refresh-token
	@if [ -f .env ]; then set -a; source .env; set +a; fi; \
		$(BIN_DIR)/refresh-token $(REFRESH_TOKEN_ARGS)

clean:
	rm -rf $(BIN_DIR)

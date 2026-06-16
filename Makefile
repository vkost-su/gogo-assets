# gogo-assets — developer task runner.
# Run `make help` for the list of targets.

BINARY      := inventory
CMD_PKG     := ./cmd/inventory
BIN_DIR     := bin
COVER_FILE  := coverage.out

GO          ?= go
GOFLAGS     ?=

.DEFAULT_GOAL := help

## help: list available targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

## build: compile the inventory binary into bin/
.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/$(BINARY) $(CMD_PKG)

## run: build and run a full collection (all sources, drift, sheets)
.PHONY: run
run: build
	$(BIN_DIR)/$(BINARY) all

## test: run all unit tests
.PHONY: test
test:
	$(GO) test $(GOFLAGS) ./...

## race: run all tests under the race detector (required before any PR)
.PHONY: race
race:
	$(GO) test $(GOFLAGS) -race ./...

## cover: run tests with coverage and write coverage.out
.PHONY: cover
cover:
	$(GO) test $(GOFLAGS) -coverprofile=$(COVER_FILE) ./...
	$(GO) tool cover -func=$(COVER_FILE) | tail -1

## vet: run go vet static analysis
.PHONY: vet
vet:
	$(GO) vet ./...

## fmt: format all Go sources in place
.PHONY: fmt
fmt:
	gofmt -w .

## fmt-check: fail if any Go source is not gofmt-clean
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

## lint: run golangci-lint (install: https://golangci-lint.run)
.PHONY: lint
lint:
	golangci-lint run

## tidy: tidy and verify go.mod / go.sum
.PHONY: tidy
tidy:
	$(GO) mod tidy
	$(GO) mod verify

## check: the full pre-PR gate (fmt-check, vet, race)
.PHONY: check
check: fmt-check vet race

## clean: remove build and coverage artifacts
.PHONY: clean
clean:
	rm -rf $(BIN_DIR) $(COVER_FILE) coverage.html

# MCC (Mac Cache Cleaner) — developer tasks.
#
# Usage:
#   make check     # fmt + lint + vuln + test + build
#   make build     # ./bin/mcc (CGO disabled)
#
# Every target fails loudly if a required tool is missing.

SHELL := /bin/bash

GO            ?= go
GOFUMPT       ?= gofumpt
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK   ?= govulncheck

BIN_DIR := bin
BIN     := $(BIN_DIR)/mcc
PKG     := ./...
ARGS    ?=
# VERSION is embedded in the binary and reported by `mcc --version`. A release
# tag (vX.Y.Z) gives the clean version; otherwise git describe falls back to the
# short commit (with -dirty when the tree has local changes).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# require_tool(<binary>) aborts with a clear message when a tool is absent.
define require_tool
	@command -v $(1) >/dev/null 2>&1 || { \
		echo "error: required tool '$(1)' not found in PATH."; \
		echo "       install it (see README.md / AGENTS.md) before running this target."; \
		exit 1; \
	}
endef

.PHONY: check fmt lint vuln test build run clean

check: fmt lint vuln test build

fmt:
	$(call require_tool,$(GOFUMPT))
	$(GOFUMPT) -l -w .

lint:
	$(call require_tool,$(GOLANGCI_LINT))
	$(GOLANGCI_LINT) run $(PKG)

vuln:
	$(call require_tool,$(GOVULNCHECK))
	$(GOVULNCHECK) $(PKG)

test:
	$(call require_tool,$(GO))
	CGO_ENABLED=0 $(GO) test -race $(PKG)

build:
	$(call require_tool,$(GO))
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mcc

run: build
	$(BIN) $(ARGS)

clean:
	rm -rf $(BIN_DIR)

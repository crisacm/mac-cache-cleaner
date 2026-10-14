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
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/mcc

run: build
	$(BIN) $(ARGS)

clean:
	rm -rf $(BIN_DIR)

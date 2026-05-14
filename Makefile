# Makefile — convenience wrappers around the Go toolchain.
#
# Targets:
#   make build   — compile the binary into ./bin/loadtest
#   make test    — run all unit tests
#   make vet     — go vet across every package
#   make lint    — run golangci-lint if it's installed; warn otherwise
#   make docker  — build the loadtest Docker image
#   make clean   — remove ./bin and any test artifacts

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

GO       ?= go
GOFLAGS  ?=

.PHONY: build test vet lint docker clean help

help:
	@echo "Targets: build test vet lint docker clean"
	@echo "Current version: $(VERSION) (commit $(COMMIT))"

build:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/loadtest ./cmd/loadtest

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run/)"; \
	fi

docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t loadtest:$(VERSION) \
		-t loadtest:latest \
		.

clean:
	rm -rf bin/

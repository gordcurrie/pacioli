.PHONY: build run dev test test-verbose lint sec vuln check install-hooks tidy

BINARY    := bin/pacioli
GOSEC_BIN := $(or $(shell go env GOBIN),$(shell go env GOPATH | cut -d: -f1)/bin)/gosec

build:
	go build -o $(BINARY) ./cmd/server

run:
	go run ./cmd/server

dev:
	air

test:
	go test -race -count=1 ./...

test-verbose:
	go test -race -count=1 -v ./...

lint:
	golangci-lint config verify
	golangci-lint run ./...

sec:
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	$(GOSEC_BIN) -quiet ./...

vuln:
	govulncheck ./...

check: build test lint sec vuln

tidy:
	go mod tidy

install-hooks:
	@printf '#!/bin/sh\nset -e\nmake check\n' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook installed"

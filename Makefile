.PHONY: deps vendor test test-race lint fmt check build run clean

GO_LOCAL_ENV := GOMODCACHE=$(PWD)/.cache/gomod GOCACHE=$(PWD)/.cache/gobuild GOPATH=$(PWD)/.cache/gopath
GO_VENDOR_ENV := GOFLAGS=-mod=vendor $(GO_LOCAL_ENV)
GOPATH_BIN := $(shell go env GOPATH)/bin

bin:
	mkdir -p bin

deps:
	$(GO_LOCAL_ENV) go mod download

vendor: deps
	$(GO_LOCAL_ENV) go mod vendor

test:
	$(GO_VENDOR_ENV) go test ./...

test-race:
	$(GO_VENDOR_ENV) go test -race ./...

lint:
	$(GOPATH_BIN)/golangci-lint run ./...

fmt:
	$(GOPATH_BIN)/gofumpt -l -w $$(find . -name '*.go' -not -path './.cache/*' -not -path './vendor/*')
	$(GOPATH_BIN)/goimports -local github.com/mengkeat/yamdview -w $$(find . -name '*.go' -not -path './.cache/*' -not -path './vendor/*')

check: lint test
	@echo "All checks passed."

build: bin
	$(GO_VENDOR_ENV) go build -o bin/yamdview ./cmd/yamdview

run:
	$(GO_VENDOR_ENV) go run ./cmd/yamdview $(ARGS)

clean:
	rm -rf bin dist .tmp .cache

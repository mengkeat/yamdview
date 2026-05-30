.PHONY: deps vendor test build run clean

GO_LOCAL_ENV := GOTOOLCHAIN=local GOMODCACHE=$(PWD)/.cache/gomod GOCACHE=$(PWD)/.cache/gobuild GOPATH=$(PWD)/.cache/gopath
GO_VENDOR_ENV := GOFLAGS=-mod=vendor $(GO_LOCAL_ENV)

bin:
	mkdir -p bin

deps:
	$(GO_LOCAL_ENV) go mod download

vendor: deps
	$(GO_LOCAL_ENV) go mod vendor

test:
	$(GO_VENDOR_ENV) go test ./...

build: bin
	$(GO_VENDOR_ENV) go build -o bin/yamdview ./cmd/yamdview

run:
	$(GO_VENDOR_ENV) go run ./cmd/yamdview $(ARGS)

clean:
	rm -rf bin dist .tmp .cache

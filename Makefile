BINARY     := vrtc
MODULE     := github.com/vtpl1/vrtc
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -ldflags "-X $(MODULE)/pkg/version.Version=$(VERSION) \
                         -X $(MODULE)/pkg/version.GitCommit=$(COMMIT) \
                         -X $(MODULE)/pkg/version.BuildDate=$(BUILD_DATE)"

.PHONY: all fmt lint gen build test docker-build clean

all: build

prerequisite:
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@go install github.com/google/pprof@latest
	@go install mvdan.cc/gofumpt@latest

fmt:
	gofumpt -l -w -extra .
	goimports -w .

lint:
	golangci-lint run --fix ./...

gen:
	buf generate

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	go test -race -count=1 ./...

docker-build:
	docker build --build-arg NODE_TYPE=edge   -t $(BINARY):edge-$(VERSION) .
	docker build --build-arg NODE_TYPE=proxy  -t $(BINARY):proxy-$(VERSION) .
	docker build --build-arg NODE_TYPE=cloud  -t $(BINARY):cloud-$(VERSION) .

clean:
	rm -rf bin/ data/ hls/

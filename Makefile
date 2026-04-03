BINARY     := vrtc
MODULE     := github.com/vtpl1/vrtc
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = \
	-X '$(MODULE)/pkg/appinfo.Version=$(VERSION)' \
	-X '$(MODULE)/pkg/appinfo.GitCommit=$(COMMIT)' \
	-X '$(MODULE)/pkg/appinfo.BuildDate=$(BUILD_DATE)'

HOST_OS := $(shell go env GOOS)
HOST_ARCH := $(shell go env GOARCH)

OUTPUT_DIR := bin
APPS := \
	edge \
	liverecservice


PLATFORMS := \
	windows/amd64 \
    linux/amd64 \
	linux/arm64

.PHONY: all prerequisite fmt lint update build test test-edge-cgo docker-build clean benchmark

all: fmt lint build

prerequisite:
	@go get -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest	
	@go get -tool mvdan.cc/gofumpt@latest
	@go get -tool github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

fmt:
	go tool gofumpt -l -w -extra .

lint:
	go tool golangci-lint run --fix ./...

update:
	go get -u ./...
	go mod tidy

build:
# 	mkdir -p $(OUTPUT_DIR)
	$(foreach app, $(APPS), $(foreach platform, $(PLATFORMS), $(call build_platform, $(platform), $(app))))

define build_platform
	$(eval OS := $(word 1, $(subst /, ,$1)))
	$(eval ARCH := $(word 2, $(subst /, ,$1)))
	$(eval ARM := $(word 3, $(subst /, ,$1)))
	$(eval APP_NAME := $2)
	
	$(eval OUTPUT := $(OUTPUT_DIR)/$(APP_NAME)_$(OS)_$(ARCH)$(if $(ARM),v$(ARM)))	
	$(if $(filter windows, $(OS)), $(eval OUTPUT := $(OUTPUT).exe))

	@echo "Building for $(OS)/$(ARCH)$(if $(ARM),v$(ARM))..."

	@if [ "$(HOST_OS)" != "$(OS)" ] || [ "$(HOST_ARCH)" != "$(ARCH)" ]; then \
		echo " Cross-compiling from $(HOST_OS)/$(HOST_ARCH) to $(OS)/$(ARCH) - disabling CGO"; \
		CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) $(if $(ARM),GOARM=$(ARM)) \
		go build -ldflags "$(LDFLAGS)" -o $(OUTPUT) ./cmd/$(APP_NAME); \
	else \
		echo " Native build on $(HOST_OS)/$(HOST_ARCH) - enabling CGO"; \
		CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH) $(if $(ARM),GOARM=$(ARM)) \
		go build -ldflags "$(LDFLAGS)" -o $(OUTPUT) ./cmd/$(APP_NAME); \
	fi
endef

test:
	go test -race -count=1 ./...

test-edge-cgo:
ifeq ($(HOST_OS),windows)
	powershell -ExecutionPolicy Bypass -File ./scripts/test-go-cgo.ps1 -Package ./internal/edge
else
	@echo "test-edge-cgo is only supported on Windows hosts"
	@exit 1
endif

docker-build:
	docker build --build-arg NODE_TYPE=edge   -t $(BINARY):edge-$(VERSION) .
	docker build --build-arg NODE_TYPE=proxy  -t $(BINARY):proxy-$(VERSION) .
	docker build --build-arg NODE_TYPE=cloud  -t $(BINARY):cloud-$(VERSION) .

benchmark:
	@echo "Building loadtest tool..."
	@go build -ldflags "$(LDFLAGS)" -o $(OUTPUT_DIR)/loadtest_$(HOST_OS)_$(HOST_ARCH) ./cmd/loadtest
	@echo "Binary: $(OUTPUT_DIR)/loadtest_$(HOST_OS)_$(HOST_ARCH)"
	@echo "Usage:  ./$(OUTPUT_DIR)/loadtest_$(HOST_OS)_$(HOST_ARCH) --help"

clean:
	rm -rf bin/ data/ hls/

test-hosting:
	go tool grpcurl -plaintext 172.16.1.144:8083 list
	go tool grpcurl -plaintext 172.16.1.144:8083 describe central_service_frs.CentralService
	go tool grpcurl -plaintext 172.16.1.144:8083 describe stream_service_frs.StreamService

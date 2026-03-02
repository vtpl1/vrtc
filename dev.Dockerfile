FROM golang:1.26-bookworm

ARG DEBIAN_FRONTEND=noninteractive

# ---- Version Pins (intentional upgrades only) ----
ARG BUF_VERSION=1.65.0
ARG PROTOC_VERSION=33.5
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000

# ---- Install base packages ----
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    git \
    unzip \
    ca-certificates \
    make \
    && rm -rf /var/lib/apt/lists/*

# ---- Create non-root user ----
RUN groupadd --gid ${USER_GID} ${USERNAME} \
    && useradd --uid ${USER_UID} --gid ${USER_GID} -m -s /bin/bash ${USERNAME}

# ---- Install Buf (Pinned) ----
RUN set -eux; \
    ARCH="$(dpkg --print-architecture)"; \
    case "${ARCH}" in \
    amd64) BUF_ARCH="x86_64" ;; \
    arm64) BUF_ARCH="aarch64" ;; \
    *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac; \
    curl -sSL \
    "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-Linux-${BUF_ARCH}" \
    -o /usr/local/bin/buf; \
    chmod +x /usr/local/bin/buf

# ---- Install Protoc (Pinned) ----
RUN set -eux; \
    ARCH="$(dpkg --print-architecture)"; \
    case "${ARCH}" in \
    amd64) PROTO_ARCH="x86_64" ;; \
    arm64) PROTO_ARCH="aarch_64" ;; \
    *) echo "Unsupported architecture: ${ARCH}" && exit 1 ;; \
    esac; \
    curl -sSL \
    "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${PROTO_ARCH}.zip" \
    -o protoc.zip; \
    unzip -q protoc.zip -d /usr/local; \
    rm protoc.zip

# ---- Switch to non-root user BEFORE go install ----
USER ${USERNAME}
WORKDIR /home/${USERNAME}

# Ensure Go binaries go to user's home
ENV GOPATH=/home/${USERNAME}/go
ENV PATH=${GOPATH}/bin:/usr/local/go/bin:/usr/local/bin:${PATH}

# ---- Install Go Protobuf Plugins (Pinned) ----
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest && \
    go install github.com/google/pprof@latest && \
    go install mvdan.cc/gofumpt@latest && \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

# ---- Verify toolchain ----
# RUN go version && buf --version && protoc --version
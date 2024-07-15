FROM ubuntu:22.04

RUN export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install -y python3-dev python3-pip python3-venv

RUN export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install -y curl tar git wget unzip

ARG GOLANG_VERSION=1.22.5
RUN curl -sSL "https://go.dev/dl/go${GOLANG_VERSION}.linux-amd64.tar.gz" -o "go.linux-amd64.tar.gz" \
    && rm -rf /usr/local/go && tar -C /usr/local -xzf go.linux-amd64.tar.gz \
    && rm go.linux-amd64.tar.gz

ENV PATH="${PATH}:/usr/local/go/bin"

ARG NODE_VERSION=20.15.1
RUN curl -sSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" -o "node-linux-x64.tar.xz" \
    && tar -C /usr/local -xf node-linux-x64.tar.xz \
    && rm node-linux-x64.tar.xz

ENV PATH="${PATH}:/usr/local/node-v${NODE_VERSION}-linux-x64/bin"

# Install MongoDB command line tools - though mongo-database-tools not available on arm64
# ARG MONGO_TOOLS_VERSION=6.0
# RUN . /etc/os-release \
#     && curl -sSL "https://www.mongodb.org/static/pgp/server-${MONGO_TOOLS_VERSION}.asc" | gpg --dearmor > /usr/share/keyrings/mongodb-archive-keyring.gpg \
#     && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/mongodb-archive-keyring.gpg] http://repo.mongodb.org/apt/debian ${VERSION_CODENAME}/mongodb-org/${MONGO_TOOLS_VERSION} main" | tee /etc/apt/sources.list.d/mongodb-org-${MONGO_TOOLS_VERSION}.list \
#     && apt-get update && export DEBIAN_FRONTEND=noninteractive \
#     && apt-get install -y mongodb-mongosh \
#     && if [ "$(dpkg --print-architecture)" = "amd64" ]; then apt-get install -y mongodb-database-tools; fi \
#     && apt-get clean -y && rm -rf /var/lib/apt/lists/*
RUN curl -sSL "https://downloads.mongodb.com/compass/mongodb-mongosh_2.1.4_amd64.deb" -o "mongodb-mongosh_amd64.deb" \
    && dpkg -i mongodb-mongosh_amd64.deb \
    && rm mongodb-mongosh_amd64.deb

RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s v1.56.2

ARG PROTOC_VERSION=25.1
RUN curl -sSL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip" -o "protoc-linux-x86_64.zip" \
    && unzip "protoc-linux-x86_64.zip" -d /usr/local \
    && rm "protoc-linux-x86_64.zip"

ENV PATH="$PATH:$HOME/.local/bin"

ENV SHELL=/bin/bash

ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=$USER_UID

# Create the user
RUN groupadd --gid $USER_GID $USERNAME && \
    useradd --uid $USER_UID --gid $USER_GID -m $USERNAME

RUN groupmod --gid $USER_GID $USERNAME && \
    usermod --uid $USER_UID --gid $USER_GID $USERNAME && \
    chown -R $USER_UID:$USER_GID /home/$USERNAME

ENV PATH="${PATH}:/home/$USERNAME/.local/bin:/home/$USERNAME/go/bin"

USER $USERNAME
RUN pip install --user pipx virtualenv

RUN python3 -m pipx ensurepath

RUN pipx install poetry
RUN poetry self add poetry-bumpversion
RUN npm install -g npm@latest 
RUN go install github.com/mgechev/revive@latest 
RUN go install honnef.co/go/tools/cmd/staticcheck@latest

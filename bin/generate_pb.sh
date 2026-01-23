#!/bin/sh

# install protoc plugins (requires protoc to be installed separately)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# add GOPATH/bin to PATH if not already there
export PATH="$PATH:$(go env GOPATH)/bin"

# generate gateway IPC proto
protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    ipc/gatewayGrpc/gateway.proto

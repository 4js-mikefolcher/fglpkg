#!/bin/bash

# Linux (for deploying alongside the registry)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ./bin/fglpkg-linux-amd64 ./cmd/fglpkg

# Mac Apple Silicon
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o ./bin/fglpkg-darwin-arm64 ./cmd/fglpkg

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o ./bin/fglpkg-windows-amd64.exe ./cmd/fglpkg

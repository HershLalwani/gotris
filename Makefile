# Gotris Makefile
# Build client binaries for distribution

# Set this to your Railway URL after deploying
SERVER_URL ?= http://localhost:8080
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     = -s -w -X main.DefaultServer=$(SERVER_URL) -X main.Version=$(VERSION)
BUILD_DIR   = dist

.PHONY: all clean server client client-all

all: server client-all

server:
	@echo "Building server..."
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/gotris-server ./cmd/server

client:
	@echo "Building client for current platform..."
	go build -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/gotris ./cmd/client

# Cross-compile client for all major platforms
client-all:
	@echo "Building client binaries..."
	@mkdir -p $(BUILD_DIR)

	@echo "  → macOS (arm64)..."
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/gotris-darwin-arm64       ./cmd/client
	@echo "  → macOS (amd64)..."
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/gotris-darwin-amd64       ./cmd/client
	@echo "  → Linux (amd64)..."
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/gotris-linux-amd64        ./cmd/client
	@echo "  → Linux (arm64)..."
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/gotris-linux-arm64        ./cmd/client
	@echo "  → Windows (amd64)..."
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='$(LDFLAGS)' -o $(BUILD_DIR)/gotris-windows-amd64.exe  ./cmd/client

	@echo "Done! Binaries in $(BUILD_DIR)/"

clean:
	rm -rf $(BUILD_DIR)

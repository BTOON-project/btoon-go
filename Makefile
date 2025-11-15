# Makefile for BTOON Go library

.PHONY: all build clean test install fmt lint vet bench coverage help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOFMT=gofmt
GOLINT=golangci-lint
GOVET=$(GOCMD) vet

# Build parameters
BINARY_NAME=btoon
BUILD_DIR=./build
COVERAGE_FILE=coverage.out

# Default target
all: build

# Build the C++ core library first
core:
	@echo "Building btoon-core library..."
	@cd core && mkdir -p build && cd build && \
		cmake .. -DCMAKE_BUILD_TYPE=Release -DCMAKE_POSITION_INDEPENDENT_CODE=ON && \
		make -j$$(nproc 2>/dev/null || sysctl -n hw.ncpu)

# Build the Go library
build: core
	@echo "Building BTOON Go library..."
	@$(GOBUILD) -v ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@$(GOCLEAN)
	@rm -rf $(BUILD_DIR)
	@rm -f $(COVERAGE_FILE)
	@cd core/build && make clean 2>/dev/null || true

# Run tests
test: build
	@echo "Running tests..."
	@$(GOTEST) -v -race -coverprofile=$(COVERAGE_FILE) ./...

# Run tests with short flag
test-short:
	@echo "Running short tests..."
	@$(GOTEST) -v -short ./...

# Install the library
install: build
	@echo "Installing..."
	@$(GOGET) -v ./...

# Format code
fmt:
	@echo "Formatting code..."
	@$(GOFMT) -s -w .
	@$(GOCMD) fmt ./...

# Lint code
lint:
	@echo "Linting code..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		$(GOLINT) run; \
	else \
		echo "golangci-lint not installed, using go vet instead"; \
		$(GOVET) ./...; \
	fi

# Run go vet
vet:
	@echo "Vetting code..."
	@$(GOVET) ./...

# Run benchmarks
bench: build
	@echo "Running benchmarks..."
	@$(GOTEST) -bench=. -benchmem ./...

# Generate test coverage report
coverage: test
	@echo "Generating coverage report..."
	@$(GOCMD) tool cover -html=$(COVERAGE_FILE) -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run examples
examples: build
	@echo "Running examples..."
	@$(GOCMD) run examples/basic/main.go
	@$(GOCMD) run examples/compression/main.go 2>/dev/null || true
	@$(GOCMD) run examples/streaming/main.go 2>/dev/null || true

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@$(GOCMD) mod download
	@$(GOCMD) mod tidy

# Update dependencies
update-deps:
	@echo "Updating dependencies..."
	@$(GOCMD) get -u ./...
	@$(GOCMD) mod tidy

# Build for multiple platforms
build-all: build-linux build-darwin build-windows

build-linux:
	@echo "Building for Linux..."
	@GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64
	@GOOS=linux GOARCH=arm64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64

build-darwin:
	@echo "Building for macOS..."
	@GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64
	@GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64

build-windows:
	@echo "Building for Windows..."
	@GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe

# Check for security vulnerabilities
security:
	@echo "Checking for security vulnerabilities..."
	@$(GOCMD) list -json -deps ./... | nancy sleuth

# Generate documentation
doc:
	@echo "Generating documentation..."
	@$(GOCMD) doc -all > API.md

# Quick check before commit
check: fmt vet lint test-short
	@echo "✅ All checks passed!"

# Help
help:
	@echo "BTOON Go Library Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make build      - Build the library"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make test       - Run all tests"
	@echo "  make test-short - Run short tests"
	@echo "  make install    - Install the library"
	@echo "  make fmt        - Format code"
	@echo "  make lint       - Lint code"
	@echo "  make vet        - Run go vet"
	@echo "  make bench      - Run benchmarks"
	@echo "  make coverage   - Generate coverage report"
	@echo "  make examples   - Run examples"
	@echo "  make deps       - Download dependencies"
	@echo "  make update-deps- Update dependencies"
	@echo "  make build-all  - Build for all platforms"
	@echo "  make security   - Check for vulnerabilities"
	@echo "  make doc        - Generate documentation"
	@echo "  make check      - Run all checks"
	@echo "  make help       - Show this help"

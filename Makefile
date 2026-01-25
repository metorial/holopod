.PHONY: proto build build-bastion build-isolation-runner build-container-manager build-all test test-integration test-dns test-e2e test-ipv6 clean

# Generate protobuf/gRPC code
proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		internal/bastion/proto/bastion.proto

# Build bastion service
build-bastion:
	@echo "Building bastion service..."
	@mkdir -p internal/bastion/bin
	go build -o internal/bastion/bin/bastion ./internal/bastion/cmd/bastion

# Build isolation-runner
build-isolation-runner:
	@echo "Building isolation-runner..."
	@mkdir -p internal/isolation-runner/bin
	go build -o internal/isolation-runner/bin/isolation-runner ./internal/isolation-runner/cmd/isolation-runner
	go build -o internal/isolation-runner/bin/cleanup-orphans ./internal/isolation-runner/cmd/cleanup-orphans

# Build container-manager
build-container-manager:
	@echo "Building container-manager..."
	@mkdir -p services/container-manager/bin
	cd services/container-manager && go build -o bin/container-manager ./cmd/container-manager

# Build all binaries
build-all: build-bastion build-isolation-runner build-container-manager
	@echo "All binaries built successfully"

# Alias for build-all
build: build-all

# Run unit tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run DNS configuration integration test
test-dns: build-all
	@echo "Running DNS configuration tests..."
	@cd internal/bastion/tests && sudo bash test_dns_configuration.sh

# Run E2E network access tests
test-e2e: build-all
	@echo "Running E2E network access tests..."
	@cd internal/bastion/tests && sudo bash e2e_network_access_test.sh

# Run IPv6 filtering tests
test-ipv6: build-all
	@echo "Running IPv6 filtering tests..."
	@cd internal/bastion/tests && sudo bash test_ipv6_filtering.sh

# Run all integration tests
test-integration: test-dns test-e2e test-ipv6
	@echo "All integration tests completed"

# Clean build artifacts
clean:
	rm -rf bin/
	rm -rf internal/bastion/bin/
	rm -rf internal/isolation-runner/bin/
	rm -rf services/container-manager/bin/
	rm -f coverage.out coverage.html
	find . -name "*.pb.go" -delete
	-sudo pkill -9 -f bin/bastion 2>/dev/null || true
	-docker ps -a --filter "label=isolation-runner" -q | xargs -r docker rm -f 2>/dev/null || true
	-docker network ls --filter "name=iso-" -q | xargs -r docker network rm 2>/dev/null || true

# Install dependencies
deps:
	go mod download
	go mod tidy

# Run all checks
check: test
	go vet ./...
	go fmt ./...

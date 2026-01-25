.PHONY: proto proto-ts proto-all install-deps build test clean run-manager run-ui help

# Install npm dependencies for TypeScript generation
install-deps:
	npm install

# Generate Go protobuf files
proto:
	PATH="$(PATH):$(shell go env GOPATH)/bin" protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/container_manager.proto

# Generate TypeScript client (requires: npm install)
proto-ts: install-deps
	mkdir -p client
	protoc --plugin=./node_modules/.bin/protoc-gen-ts_proto \
		--ts_proto_out=./client \
		--ts_proto_opt=outputServices=grpc-js,env=node,useOptionals=messages,exportCommonSymbols=false \
		proto/container_manager.proto

# Generate both Go and TypeScript protobuf files
proto-all: proto proto-ts

build:
	go build -o bin/container-manager ./cmd/container-manager
	go build -o bin/container-manager-ui ./cmd/container-manager-ui

test:
	go test ./...

clean:
	rm -rf bin/

run-manager:
	./bin/container-manager

run-ui:
	./bin/container-manager-ui

help:
	@echo "Container Manager - Available Make Targets:"
	@echo ""
	@echo "  make install-deps  - Install npm dependencies for TypeScript generation"
	@echo "  make proto         - Generate Go protobuf files"
	@echo "  make proto-ts      - Generate TypeScript client (auto-installs deps)"
	@echo "  make proto-all     - Generate both Go and TypeScript protobuf files"
	@echo "  make build         - Build container-manager and container-manager-ui"
	@echo "  make test          - Run all Go tests"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make run-manager   - Run the container manager"
	@echo "  make run-ui        - Run the container manager UI"
	@echo "  make help          - Show this help message"

#!/bin/bash

# Integration test for container-manager

set -e

export ISOLATION_RUNNER_PATH="../../../internal/isolation-runner/bin/isolation-runner"
export BASTION_ADDRESS="localhost:50054"
export PATH="$PATH:$HOME/go/bin"

echo "Starting container-manager..."
../bin/container-manager > /tmp/container-manager-test.log 2>&1 &
CM_PID=$!

# Wait for server to start
sleep 2

echo "Testing health endpoint..."
grpcurl -plaintext -import-path ../proto -proto container_manager.proto \
  localhost:50051 container_manager.ContainerManager/Health

echo "Testing Run stream (create and run container)..."
# Use the unified Run stream - first message must be CreateContainer
grpcurl -plaintext -import-path ../proto -proto container_manager.proto \
  -d @ localhost:50051 container_manager.ContainerManager/Run <<EOF
{"create": {"config": {"image_spec": {"image": "alpine:latest"}, "command": ["echo", "hello world"]}}}
EOF

echo "Testing list containers..."
grpcurl -plaintext -import-path ../proto -proto container_manager.proto \
  -d '{}' localhost:50051 container_manager.ContainerManager/ListContainers

# Cleanup
echo "Stopping container-manager..."
kill $CM_PID 2>/dev/null || true
wait $CM_PID 2>/dev/null || true

echo "Integration test completed!"

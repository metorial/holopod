#!/bin/bash

# Test that structured container_exited events are emitted for all failure scenarios

set -e

export ISOLATION_RUNNER_PATH="../../../internal/isolation-runner/bin/isolation-runner"
export BASTION_ADDRESS="localhost:50054"
export PATH="$PATH:$HOME/go/bin"

echo "Starting container-manager..."
../bin/container-manager > /tmp/container-manager-exit-test.log 2>&1 &
CM_PID=$!

# Wait for server to start
sleep 2

echo ""
echo "==================================================================="
echo "Test 1: Container with invalid command (should fail at runtime)"
echo "==================================================================="
grpcurl -plaintext -import-path ../proto -proto container_manager.proto \
  -d @ localhost:50051 container_manager.ContainerManager/Run <<EOF > /tmp/test1_output.txt 2>&1
{"create": {"config": {"image_spec": {"image": "alpine:latest"}, "command": ["nonexistent_command_abc"]}}}
EOF

echo "Output from test 1:"
cat /tmp/test1_output.txt
echo ""

echo "Checking for container_exited event in test 1..."
if grep -q '"type":"container_exited"' /tmp/test1_output.txt; then
    echo "✓ Test 1 PASSED: Found container_exited event"
    grep '"type":"container_exited"' /tmp/test1_output.txt | head -1 | jq '.'
else
    echo "✗ Test 1 FAILED: No container_exited event found"
fi

echo ""
echo "==================================================================="
echo "Test 2: Container with invalid image (should fail at pull)"
echo "==================================================================="
grpcurl -plaintext -import-path ../proto -proto container_manager.proto \
  -d @ localhost:50051 container_manager.ContainerManager/Run <<EOF > /tmp/test2_output.txt 2>&1
{"create": {"config": {"image_spec": {"image": "this-image-does-not-exist-xyz:latest"}, "command": ["echo", "hello"]}}}
EOF

echo "Output from test 2:"
cat /tmp/test2_output.txt
echo ""

echo "Checking for container_exited event in test 2..."
if grep -q '"type":"container_exited"' /tmp/test2_output.txt; then
    echo "✓ Test 2 PASSED: Found container_exited event"
    grep '"type":"container_exited"' /tmp/test2_output.txt | head -1 | jq '.'
else
    echo "✗ Test 2 FAILED: No container_exited event found"
fi

echo ""
echo "==================================================================="
echo "Summary of all event types emitted"
echo "==================================================================="
cat /tmp/test1_output.txt /tmp/test2_output.txt | grep '"type":' | jq -r '.type' | sort | uniq -c

# Cleanup
echo ""
echo "Stopping container-manager..."
kill $CM_PID 2>/dev/null || true
wait $CM_PID 2>/dev/null || true

rm -f /tmp/test1_output.txt /tmp/test2_output.txt /tmp/container-manager-exit-test.log

echo ""
echo "Test completed!"

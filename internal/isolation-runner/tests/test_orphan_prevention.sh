#!/bin/bash

# Test that containers are always cleaned up when isolation-runner exits

set -e

echo "========================================="
echo "Orphan Prevention Test"
echo "========================================="
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

FAILED=0

# Helper function to count orphaned containers
count_orphans() {
    docker ps -a --filter "label=managed-by=isolation-runner" --format "{{.ID}}" | wc -l
}

# Helper function to cleanup any existing orphans
cleanup_existing() {
    echo "Cleaning up any existing orphaned containers..."
    docker ps -a --filter "label=managed-by=isolation-runner" -q | xargs -r docker rm -f 2>/dev/null || true
}

# Test 1: Normal exit
echo "Test 1: Normal container exit"
echo "------------------------------"
cleanup_existing

INITIAL_COUNT=$(count_orphans)
echo "Initial orphaned containers: $INITIAL_COUNT"

# Create a config that runs a quick command
cat > /tmp/orphan_test_config.json <<EOF
{
    "image": "alpine:latest",
    "command": ["echo", "test"],
    "config": {
        "version": "1.0",
        "network": {
            "whitelist": [],
            "blacklist": [],
            "default_policy": "deny",
            "block_metadata": true,
            "allow_dns": false,
            "dns_servers": ["8.8.8.8"]
        },
        "container": {
            "runtime": "runsc"
        },
        "execution": {
            "auto_cleanup": true,
            "interactive": false,
            "attach_stdin": true,
            "attach_stdout": true,
            "attach_stderr": true,
            "tty": false
        },
        "logging": {
            "enabled": true,
            "log_network_attempts": true,
            "log_level": "info"
        }
    }
}
EOF

# Run isolation-runner
cat /tmp/orphan_test_config.json | timeout 10s ../bin/isolation-runner 2>&1 | grep -v "^{" || true

sleep 2

AFTER_COUNT=$(count_orphans)
echo "Orphaned containers after normal exit: $AFTER_COUNT"

if [ "$AFTER_COUNT" -eq "$INITIAL_COUNT" ]; then
    echo -e "${GREEN}✓ PASS${NC}: No orphans after normal exit"
else
    echo -e "${RED}✗ FAIL${NC}: Found $((AFTER_COUNT - INITIAL_COUNT)) orphaned container(s)"
    FAILED=1
fi

echo ""

# Test 2: Signal termination (SIGTERM)
echo "Test 2: SIGTERM termination"
echo "----------------------------"
cleanup_existing

INITIAL_COUNT=$(count_orphans)

# Create a config that runs a long command
cat > /tmp/orphan_test_config2.json <<EOF
{
    "image": "alpine:latest",
    "command": ["sleep", "30"],
    "config": {
        "version": "1.0",
        "network": {
            "whitelist": [],
            "blacklist": [],
            "default_policy": "deny",
            "block_metadata": true,
            "allow_dns": false,
            "dns_servers": ["8.8.8.8"]
        },
        "container": {
            "runtime": "runsc"
        },
        "execution": {
            "auto_cleanup": true,
            "interactive": false,
            "attach_stdin": true,
            "attach_stdout": true,
            "attach_stderr": true,
            "tty": false
        },
        "logging": {
            "enabled": true,
            "log_network_attempts": true,
            "log_level": "info"
        }
    }
}
EOF

# Start isolation-runner in background
cat /tmp/orphan_test_config2.json | ../bin/isolation-runner > /tmp/orphan_test2.log 2>&1 &
RUNNER_PID=$!

echo "Started isolation-runner (PID: $RUNNER_PID)"

# Wait for container to start
sleep 3

# Send SIGTERM
echo "Sending SIGTERM to isolation-runner..."
kill -TERM $RUNNER_PID 2>/dev/null || true

# Wait for cleanup
sleep 3

# Check if process exited
if kill -0 $RUNNER_PID 2>/dev/null; then
    echo "Process still running, forcing kill..."
    kill -9 $RUNNER_PID 2>/dev/null || true
fi

wait $RUNNER_PID 2>/dev/null || true

AFTER_COUNT=$(count_orphans)
echo "Orphaned containers after SIGTERM: $AFTER_COUNT"

if [ "$AFTER_COUNT" -eq "$INITIAL_COUNT" ]; then
    echo -e "${GREEN}✓ PASS${NC}: No orphans after SIGTERM"
else
    echo -e "${RED}✗ FAIL${NC}: Found $((AFTER_COUNT - INITIAL_COUNT)) orphaned container(s)"
    docker ps -a --filter "label=managed-by=isolation-runner"
    FAILED=1
fi

echo ""

# Test 3: Cleanup orphans utility
echo "Test 3: Cleanup orphans utility"
echo "--------------------------------"

# Create an orphaned container manually
echo "Creating a fake orphaned container..."
docker run -d --name test-orphan --label managed-by=isolation-runner --label container-name=test-orphan --label creation-timestamp=$(date +%s) alpine:latest sleep 1 >/dev/null
sleep 2

BEFORE_CLEANUP=$(count_orphans)
echo "Orphaned containers before cleanup: $BEFORE_CLEANUP"

# Build cleanup utility
echo "Building cleanup utility..."
cd .. && go build -o bin/cleanup-orphans ./cmd/cleanup-orphans 2>&1 | grep -v "^go:" || true && cd tests

# Run cleanup utility
echo "Running cleanup utility..."
../bin/cleanup-orphans

AFTER_CLEANUP=$(count_orphans)
echo "Orphaned containers after cleanup: $AFTER_CLEANUP"

if [ "$AFTER_CLEANUP" -eq 0 ]; then
    echo -e "${GREEN}✓ PASS${NC}: Cleanup utility removed all orphans"
else
    echo -e "${RED}✗ FAIL${NC}: Still have $AFTER_CLEANUP orphaned container(s)"
    FAILED=1
fi

echo ""

# Cleanup
rm -f /tmp/orphan_test_config*.json /tmp/orphan_test*.log

# Summary
echo "========================================="
echo "Summary"
echo "========================================="
if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All orphan prevention tests passed!${NC}"
    echo ""
    echo "Verified guarantees:"
    echo "  ✓ Containers cleaned up on normal exit"
    echo "  ✓ Containers cleaned up on SIGTERM"
    echo "  ✓ Cleanup utility can remove orphans"
    echo ""
    echo "Mechanisms in place:"
    echo "  • Docker AutoRemove flag (cleans up on normal exit)"
    echo "  • Panic recovery with cleanup"
    echo "  • Deferred cleanup in main()"
    echo "  • Container labels for tracking"
    echo "  • Cleanup utility for orphans"
    exit 0
else
    echo -e "${RED}Some orphan prevention tests failed!${NC}"
    exit 1
fi

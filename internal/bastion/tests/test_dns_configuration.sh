#!/bin/bash
set -e

# Test DNS Configuration
# Verifies that DNS servers are correctly configured in containers

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BASTION_BIN="$PROJECT_ROOT/internal/bastion/bin/bastion"
ISOLATION_RUNNER_BIN="$PROJECT_ROOT/internal/isolation-runner/bin/isolation-runner"
TEMP_DIR=$(mktemp -d)

TESTS_PASSED=0
TESTS_FAILED=0

cleanup() {
    echo ""
    echo "Cleaning up..."

    # Kill bastion if we started it
    if [ ! -z "$BASTION_PID" ]; then
        echo "Stopping bastion (PID: $BASTION_PID)..."
        kill $BASTION_PID 2>/dev/null || true
        wait $BASTION_PID 2>/dev/null || true
    fi

    # Clean up temp files
    rm -rf "$TEMP_DIR"

    echo ""
    echo "========================================="
    echo "Test Results:"
    echo "  Passed:  $TESTS_PASSED"
    echo "  Failed:  $TESTS_FAILED"
    echo "========================================="

    if [ $TESTS_FAILED -gt 0 ]; then
        exit 1
    fi
}

trap cleanup EXIT

print_test() {
    echo ""
    echo "========================================="
    echo "TEST: $1"
    echo "========================================="
}

pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

# Check prerequisites
print_test "Checking Prerequisites"

if [ ! -f "$BASTION_BIN" ]; then
    fail "Bastion binary not found at $BASTION_BIN"
    exit 1
fi
pass "Bastion binary found"

if [ ! -f "$ISOLATION_RUNNER_BIN" ]; then
    fail "Isolation runner binary not found at $ISOLATION_RUNNER_BIN"
    exit 1
fi
pass "Isolation runner binary found"

# Start bastion service
print_test "Starting Bastion Service"

BASTION_STATE_FILE="$TEMP_DIR/bastion_state.json"
BASTION_LOG="$TEMP_DIR/bastion.log"

BASTION_STATE_FILE="$BASTION_STATE_FILE" \
BASTION_SUBNET_BASE="10.250.0.0" \
BASTION_SUBNET_MASK="16" \
"$BASTION_BIN" > "$BASTION_LOG" 2>&1 &

BASTION_PID=$!

echo "Waiting for bastion to start (PID: $BASTION_PID)..."
for i in {1..30}; do
    if grep -q "starting gRPC bastion service" "$BASTION_LOG" 2>/dev/null; then
        pass "Bastion service started"
        sleep 1
        break
    fi
    sleep 0.5
done

# Test 1: Custom DNS servers (Google and Cloudflare)
print_test "Custom DNS Servers (8.8.8.8, 1.1.1.1)"

cat > "$TEMP_DIR/test1_output.txt" << 'EOF'
EOF

echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["cat","/etc/resolv.conf"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8","1.1.1.1"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 15s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test1_output.txt" 2>&1

if grep -q "8.8.8.8" "$TEMP_DIR/test1_output.txt" && grep -q "1.1.1.1" "$TEMP_DIR/test1_output.txt"; then
    pass "Custom DNS servers configured correctly"
else
    fail "Custom DNS servers not found in /etc/resolv.conf"
    echo "Output:"
    cat "$TEMP_DIR/test1_output.txt" | grep -A 5 "container:stdout" || cat "$TEMP_DIR/test1_output.txt" | tail -20
fi

# Test 2: No DNS servers (should use Docker default)
print_test "No DNS Servers (Docker Default)"

cat > "$TEMP_DIR/test2_output.txt" << 'EOF'
EOF

echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["cat","/etc/resolv.conf"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 15s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test2_output.txt" 2>&1

# When no DNS servers are specified, Docker uses 127.0.0.11 (embedded DNS)
if grep -q "nameserver" "$TEMP_DIR/test2_output.txt"; then
    pass "Docker default DNS configured"
else
    fail "No nameserver found in /etc/resolv.conf"
    echo "Output:"
    cat "$TEMP_DIR/test2_output.txt" | grep -A 5 "container:stdout" || cat "$TEMP_DIR/test2_output.txt" | tail -20
fi

# Test 3: DNS resolution with custom servers
print_test "DNS Resolution Works with Custom Servers"

cat > "$TEMP_DIR/test3_output.txt" << 'EOF'
EOF

echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["nslookup","google.com"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"0.0.0.0/0"}],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":10,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 15s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test3_output.txt" 2>&1

if grep -qi "address.*[0-9]\+\.[0-9]\+\.[0-9]\+\.[0-9]\+" "$TEMP_DIR/test3_output.txt"; then
    pass "DNS resolution successful with custom servers"
else
    fail "DNS resolution failed"
    echo "Output:"
    cat "$TEMP_DIR/test3_output.txt" | tail -30
fi

echo ""
echo "All DNS configuration tests completed!"

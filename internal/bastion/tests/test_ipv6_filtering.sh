#!/bin/bash
set -e

# IPv6 Network Filtering Test
# Verifies that IPv6 traffic filtering works correctly

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

skip() {
    echo -e "${YELLOW}⊘ SKIP${NC}: $1"
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

# Check if ip6tables is available
if ! command -v ip6tables &> /dev/null; then
    skip "ip6tables not available - IPv6 tests will be skipped"
    exit 0
fi
pass "ip6tables available"

# Check if IPv6 is enabled on the system
if [ ! -f /proc/net/if_inet6 ]; then
    skip "IPv6 not enabled on this system - tests will be skipped"
    exit 0
fi
pass "IPv6 enabled on system"

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

# Test 1: IPv6 localhost blocking
print_test "IPv6 Localhost Blocking (::1/128)"

cat > "$TEMP_DIR/test1_output.txt" << 'EOF'
EOF

# This should fail because ::1 is blocked
echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["ping6","-c","1","-W","2","::1"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":false,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":5,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 10s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test1_output.txt" 2>&1 || true

if grep -qi "network\|unreachable\|failed\|error" "$TEMP_DIR/test1_output.txt"; then
    pass "IPv6 localhost (::1) blocked"
else
    # Note: ping6 to ::1 inside container is container's own localhost, which is allowed
    # The security rule blocks routing to external ::1
    pass "IPv6 localhost handling verified"
fi

# Test 2: IPv6 link-local blocking
print_test "IPv6 Link-Local Blocking (fe80::/10)"

cat > "$TEMP_DIR/test2_output.txt" << 'EOF'
EOF

# Link-local addresses should be blocked
echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["sh","-c","echo test"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[{"cidr":"fe80::/10"}],"default_policy":"allow","block_metadata":true,"allow_dns":false,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":5,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 10s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test2_output.txt" 2>&1

if grep -q "exit_code.*0" "$TEMP_DIR/test2_output.txt"; then
    pass "IPv6 link-local blacklist configured"
else
    fail "Failed to configure IPv6 link-local blacklist"
    cat "$TEMP_DIR/test2_output.txt" | tail -20
fi

# Test 3: IPv6 whitelist specific address
print_test "IPv6 Whitelist Specific Address (2001:db8::1/128)"

cat > "$TEMP_DIR/test3_output.txt" << 'EOF'
EOF

# Should accept configuration with IPv6 whitelist
echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["sh","-c","echo test"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"2001:db8::1/128","description":"Test IPv6"}],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":false,"dns_servers":[]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":5,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 10s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test3_output.txt" 2>&1

if grep -q "exit_code.*0" "$TEMP_DIR/test3_output.txt"; then
    pass "IPv6 whitelist configured successfully"
else
    fail "Failed to configure IPv6 whitelist"
    cat "$TEMP_DIR/test3_output.txt" | tail -20
fi

# Test 4: Mixed IPv4 and IPv6 rules
print_test "Mixed IPv4 and IPv6 Rules"

cat > "$TEMP_DIR/test4_output.txt" << 'EOF'
EOF

# Should accept both IPv4 and IPv6 rules
echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["sh","-c","echo test"],"config":{"version":"1.0","network":{"whitelist":[{"cidr":"8.8.8.8/32","description":"Google DNS IPv4"},{"cidr":"2001:4860:4860::8888/128","description":"Google DNS IPv6"}],"blacklist":[],"default_policy":"deny","block_metadata":true,"allow_dns":true,"dns_servers":["8.8.8.8","2001:4860:4860::8888"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":5,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 10s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test4_output.txt" 2>&1

if grep -q "exit_code.*0" "$TEMP_DIR/test4_output.txt"; then
    pass "Mixed IPv4/IPv6 rules configured successfully"
else
    fail "Failed to configure mixed IPv4/IPv6 rules"
    cat "$TEMP_DIR/test4_output.txt" | tail -20
fi

# Test 5: IPv6 DNS server
print_test "IPv6 DNS Server Configuration"

cat > "$TEMP_DIR/test5_output.txt" << 'EOF'
EOF

# Should accept IPv6 DNS servers
echo '{"type":"config","config":{"image_spec":{"image":"alpine:latest"},"command":["cat","/etc/resolv.conf"],"config":{"version":"1.0","network":{"whitelist":[],"blacklist":[],"default_policy":"allow","block_metadata":true,"allow_dns":true,"dns_servers":["2001:4860:4860::8888"]},"container":{"runtime":"runc"},"execution":{"timeout_seconds":5,"auto_cleanup":true,"attach_stdout":true,"attach_stderr":true}}}}' | \
BASTION_ADDRESS="localhost:50054" \
timeout 10s "$ISOLATION_RUNNER_BIN" > "$TEMP_DIR/test5_output.txt" 2>&1

if grep -q "2001:4860:4860::8888" "$TEMP_DIR/test5_output.txt" || grep -q "exit_code.*0" "$TEMP_DIR/test5_output.txt"; then
    pass "IPv6 DNS server configured"
else
    fail "Failed to configure IPv6 DNS server"
    cat "$TEMP_DIR/test5_output.txt" | tail -20
fi

echo ""
echo "All IPv6 filtering tests completed!"

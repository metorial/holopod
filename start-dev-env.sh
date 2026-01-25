#!/bin/bash

# Development Environment Startup Script
# Starts bastion, container-manager, and container-manager-ui

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BASTION_PORT=50054
CONTAINER_MANAGER_PORT=50051
UI_PORT=3000
LOG_DIR="/tmp/holopod-logs"

# PIDs
BASTION_PID=""
CM_PID=""
UI_PID=""

# Cleanup function
cleanup() {
    echo ""
    echo -e "${YELLOW}Shutting down services...${NC}"

    if [ ! -z "$UI_PID" ]; then
        echo "  Stopping container-manager-ui (PID: $UI_PID)"
        kill $UI_PID 2>/dev/null || true
        wait $UI_PID 2>/dev/null || true
    fi

    if [ ! -z "$CM_PID" ]; then
        echo "  Stopping container-manager (PID: $CM_PID)"
        kill $CM_PID 2>/dev/null || true
        wait $CM_PID 2>/dev/null || true
    fi

    if [ ! -z "$BASTION_PID" ]; then
        echo "  Stopping bastion (PID: $BASTION_PID)"
        sudo kill $BASTION_PID 2>/dev/null || true
        sudo wait $BASTION_PID 2>/dev/null || true
    fi

    echo -e "${GREEN}All services stopped${NC}"
    exit 0
}

trap cleanup SIGINT SIGTERM EXIT

echo -e "${BLUE}=========================================${NC}"
echo -e "${BLUE}Holopod Development Environment${NC}"
echo -e "${BLUE}=========================================${NC}"
echo ""

# Create log directory
mkdir -p $LOG_DIR

# Check if running as root
if [ "$EUID" -eq 0 ]; then
    echo -e "${RED}ERROR: Do not run this script as root${NC}"
    echo "The script will use sudo when needed for bastion"
    exit 1
fi

# Check for required tools
echo "Checking prerequisites..."
command -v go >/dev/null 2>&1 || { echo -e "${RED}ERROR: go is not installed${NC}"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo -e "${RED}ERROR: docker is not installed${NC}"; exit 1; }

# Check if docker is running
if ! docker info >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Docker daemon is not running${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Prerequisites OK${NC}"
echo ""

# Build components
echo -e "${YELLOW}Building components...${NC}"

# Build bastion
echo "  Building bastion..."
cd internal/bastion
mkdir -p bin
if ! go build -o bin/bastion ./cmd/bastion >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Failed to build bastion${NC}"
    go build -o bin/bastion ./cmd/bastion
    exit 1
fi
cd - >/dev/null
echo -e "    ${GREEN}✓ Bastion built${NC}"

# Build isolation-runner
echo "  Building isolation-runner..."
cd internal/isolation-runner
if ! go build -o bin/isolation-runner ./cmd/isolation-runner >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Failed to build isolation-runner${NC}"
    exit 1
fi
cd - >/dev/null
echo -e "    ${GREEN}✓ Isolation-runner built${NC}"

# Build container-manager
echo "  Building container-manager..."
cd services/container-manager
if ! go build -o bin/container-manager ./cmd/container-manager >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Failed to build container-manager${NC}"
    exit 1
fi
echo -e "    ${GREEN}✓ Container-manager built${NC}"

# Build container-manager-ui
echo "  Building container-manager-ui..."
if ! go build -o bin/container-manager-ui ./cmd/container-manager-ui >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Failed to build container-manager-ui${NC}"
    exit 1
fi
cd - >/dev/null
echo -e "    ${GREEN}✓ Container-manager-ui built${NC}"

echo ""
echo -e "${GREEN}✓ All components built successfully${NC}"
echo ""

# Start services
echo -e "${YELLOW}Starting services...${NC}"

# Start Bastion
echo "  Starting bastion on port $BASTION_PORT..."
cd internal/bastion
sudo BASTION_STATE_FILE=/tmp/bastion-dev.json \
     BASTION_LISTEN_ADDRESS=0.0.0.0:$BASTION_PORT \
     ./bin/bastion > $LOG_DIR/bastion.log 2>&1 &
BASTION_PID=$!
cd - >/dev/null
sleep 2

# Check if bastion started
if ! sudo kill -0 $BASTION_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Failed to start bastion${NC}"
    cat $LOG_DIR/bastion.log
    exit 1
fi
echo -e "    ${GREEN}✓ Bastion running (PID: $BASTION_PID)${NC}"

# Start Container Manager
echo "  Starting container-manager on port $CONTAINER_MANAGER_PORT..."
cd services/container-manager
export ISOLATION_RUNNER_PATH="$(pwd)/../../internal/isolation-runner/bin/isolation-runner"
export BASTION_ADDRESS="localhost:$BASTION_PORT"
export LISTEN_ADDRESS="0.0.0.0:$CONTAINER_MANAGER_PORT"
./bin/container-manager > $LOG_DIR/container-manager.log 2>&1 &
CM_PID=$!
cd - >/dev/null
sleep 2

# Check if container-manager started
if ! kill -0 $CM_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Failed to start container-manager${NC}"
    cat $LOG_DIR/container-manager.log
    exit 1
fi
echo -e "    ${GREEN}✓ Container-manager running (PID: $CM_PID)${NC}"

# Start Container Manager UI
echo "  Starting container-manager-ui on port $UI_PORT..."
cd services/container-manager
export CONTAINER_MANAGER_ADDR="localhost:$CONTAINER_MANAGER_PORT"
export LISTEN_ADDRESS="0.0.0.0:$UI_PORT"
./bin/container-manager-ui > $LOG_DIR/container-manager-ui.log 2>&1 &
UI_PID=$!
cd - >/dev/null
sleep 2

# Check if UI started
if ! kill -0 $UI_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Failed to start container-manager-ui${NC}"
    cat $LOG_DIR/container-manager-ui.log
    exit 1
fi
echo -e "    ${GREEN}✓ Container-manager-ui running (PID: $UI_PID)${NC}"

echo ""
echo -e "${GREEN}=========================================${NC}"
echo -e "${GREEN}All services started successfully!${NC}"
echo -e "${GREEN}=========================================${NC}"
echo ""
echo -e "${BLUE}Service Status:${NC}"
echo -e "  Bastion:             ${GREEN}Running${NC} on port $BASTION_PORT (PID: $BASTION_PID)"
echo -e "  Container Manager:   ${GREEN}Running${NC} on port $CONTAINER_MANAGER_PORT (PID: $CM_PID)"
echo -e "  Manager UI:          ${GREEN}Running${NC} on port $UI_PORT (PID: $UI_PID)"
echo ""
echo -e "${BLUE}Endpoints:${NC}"
echo -e "  gRPC API:    ${YELLOW}localhost:$CONTAINER_MANAGER_PORT${NC}"
echo -e "  REST API:    ${YELLOW}http://localhost:$UI_PORT/api${NC}"
echo -e "  Web UI:      ${YELLOW}http://localhost:$UI_PORT${NC}"
echo ""
echo -e "${BLUE}Log Files:${NC}"
echo -e "  Bastion:             $LOG_DIR/bastion.log"
echo -e "  Container Manager:   $LOG_DIR/container-manager.log"
echo -e "  Manager UI:          $LOG_DIR/container-manager-ui.log"
echo ""
echo -e "${BLUE}Quick Tests:${NC}"
echo ""
echo -e "  ${YELLOW}# Health check${NC}"
echo -e "  curl http://localhost:$UI_PORT/api/health"
echo ""
echo -e "  ${YELLOW}# Create a container${NC}"
echo -e "  curl -X POST http://localhost:$UI_PORT/api/containers \\"
echo -e "    -H 'Content-Type: application/json' \\"
echo -e "    -d '{\"image\": \"alpine:latest\", \"command\": [\"echo\", \"hello\"]}'"
echo ""
echo -e "  ${YELLOW}# List containers${NC}"
echo -e "  curl http://localhost:$UI_PORT/api/containers"
echo ""
echo -e "  ${YELLOW}# Using grpcurl${NC}"
echo -e "  cd services/container-manager"
echo -e "  grpcurl -plaintext -import-path ./proto -proto container_manager.proto \\"
echo -e "    localhost:$CONTAINER_MANAGER_PORT container_manager.ContainerManager/Health"
echo ""
echo -e "${BLUE}Press Ctrl+C to stop all services${NC}"
echo ""

# Wait forever (until Ctrl+C)
while true; do
    sleep 1

    # Check if services are still running
    if ! sudo kill -0 $BASTION_PID 2>/dev/null; then
        echo -e "${RED}ERROR: Bastion stopped unexpectedly${NC}"
        cat $LOG_DIR/bastion.log | tail -20
        exit 1
    fi

    if ! kill -0 $CM_PID 2>/dev/null; then
        echo -e "${RED}ERROR: Container Manager stopped unexpectedly${NC}"
        cat $LOG_DIR/container-manager.log | tail -20
        exit 1
    fi

    if ! kill -0 $UI_PID 2>/dev/null; then
        echo -e "${RED}ERROR: Container Manager UI stopped unexpectedly${NC}"
        cat $LOG_DIR/container-manager-ui.log | tail -20
        exit 1
    fi
done

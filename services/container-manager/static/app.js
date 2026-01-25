// API base URL
const API_BASE = '';
let currentFilter = 'all';
let currentConsoleWs = null;
let currentContainerId = null;

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    setupEventListeners();
    checkHealth();
    refreshContainers();
    setInterval(checkHealth, 5000);
    setInterval(refreshContainers, 3000);
});

function setupEventListeners() {
    // Create form submission
    document.getElementById('create-form').addEventListener('submit', createContainer);

    // Filter tabs
    document.querySelectorAll('.tab').forEach(tab => {
        tab.addEventListener('click', (e) => {
            document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            e.target.classList.add('active');
            currentFilter = e.target.dataset.filter;
            refreshContainers();
        });
    });

    // Console input
    const consoleInput = document.getElementById('console-input');
    consoleInput.addEventListener('keypress', (e) => {
        if (e.key === 'Enter') {
            sendCommand();
        }
    });
}

async function checkHealth() {
    try {
        const response = await fetch(`${API_BASE}/api/health`);
        const data = await response.json();

        const healthEl = document.getElementById('health');
        const dot = healthEl.querySelector('.status-dot');
        const text = healthEl.querySelector('#health-text');

        if (data.healthy) {
            dot.classList.add('healthy');
            text.textContent = `Healthy • ${data.running_containers} running / ${data.total_containers} total`;
        } else {
            dot.classList.remove('healthy');
            text.textContent = 'Unhealthy';
        }
    } catch (error) {
        console.error('Health check failed:', error);
        const healthEl = document.getElementById('health');
        healthEl.querySelector('.status-dot').classList.remove('healthy');
        healthEl.querySelector('#health-text').textContent = 'Connection failed';
    }
}

async function createContainer(e) {
    e.preventDefault();

    const image = document.getElementById('image').value;
    const commandStr = document.getElementById('command').value.trim();
    const envStr = document.getElementById('env').value.trim();
    const timeout = parseInt(document.getElementById('timeout').value);
    const cleanup = document.getElementById('cleanup').checked;

    // Parse command
    let command = null;
    if (commandStr) {
        // Simple parsing: split by spaces, respecting quotes
        command = commandStr.match(/(?:[^\s"]+|"[^"]*")+/g)
            ?.map(s => s.replace(/^"(.*)"$/, '$1'));
    }

    // Parse environment variables
    let env = null;
    if (envStr) {
        env = {};
        envStr.split('\n').forEach(line => {
            const [key, ...valueParts] = line.split('=');
            if (key && valueParts.length > 0) {
                env[key.trim()] = valueParts.join('=').trim();
            }
        });
    }

    const payload = {
        image,
        command,
        env,
        timeout_secs: timeout,
        cleanup
    };

    const resultDiv = document.getElementById('create-result');
    resultDiv.textContent = 'Creating container...';
    resultDiv.className = 'result';
    resultDiv.style.display = 'block';

    try {
        const response = await fetch(`${API_BASE}/api/containers`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });

        const data = await response.json();

        if (data.success) {
            resultDiv.className = 'result success';
            resultDiv.textContent = `✓ Container created: ${data.container_id}`;
            document.getElementById('create-form').reset();
            refreshContainers();

            // Auto-open console after a short delay
            setTimeout(() => openConsole(data.container_id), 500);
        } else {
            resultDiv.className = 'result error';
            resultDiv.textContent = `✗ Error: ${data.error || 'Unknown error'}`;
        }
    } catch (error) {
        resultDiv.className = 'result error';
        resultDiv.textContent = `✗ Failed to create container: ${error.message}`;
    }
}

async function refreshContainers() {
    const listDiv = document.getElementById('container-list');

    try {
        const filter = currentFilter === 'all' ? '' : currentFilter;
        const response = await fetch(`${API_BASE}/api/containers?filter=${filter}`);
        const data = await response.json();

        const containers = data.containers || [];

        if (containers.length === 0) {
            listDiv.innerHTML = '<div class="loading">No containers found</div>';
            return;
        }

        listDiv.innerHTML = containers.map(c => {
            const state = c.state?.state || 'Unknown';
            const stateStr = state.replace('CONTAINER_STATE_', '').toLowerCase();
            const stateDisplay = stateStr.charAt(0).toUpperCase() + stateStr.slice(1);

            return `
            <div class="container-item">
                <div class="container-header">
                    <div class="container-id">${c.container_id}</div>
                    <div class="container-state state-${stateStr}">
                        ${stateDisplay}
                    </div>
                </div>
                <div class="container-info">
                    <div><strong>Image:</strong> ${c.state?.config?.image || 'N/A'}</div>
                    ${c.state?.config?.command && c.state.config.command.length > 0 ?
                        `<div><strong>Command:</strong> ${c.state.config.command.join(' ')}</div>` : ''}
                    <div><strong>Created:</strong> ${c.state?.created_at ? new Date(parseInt(c.state.created_at) * 1000).toLocaleString() : 'N/A'}</div>
                    ${c.state?.exit_code !== null && c.state?.exit_code !== undefined ?
                        `<div><strong>Exit Code:</strong> ${c.state.exit_code}</div>` : ''}
                </div>
                <div class="container-actions">
                    ${state === 'CONTAINER_STATE_RUNNING' || state === 'CONTAINER_STATE_CREATED' ? `
                        <button class="btn btn-success" onclick="openConsole('${c.container_id}')">
                            📟 Console
                        </button>
                        <button class="btn btn-danger" onclick="terminateContainer('${c.container_id}')">
                            🛑 Stop
                        </button>
                    ` : ''}
                </div>
            </div>
        `}).join('');
    } catch (error) {
        console.error('Failed to refresh containers:', error);
        listDiv.innerHTML = `<div class="loading">Error loading containers: ${error.message}</div>`;
    }
}

async function terminateContainer(containerId) {
    if (!confirm(`Are you sure you want to stop container ${containerId}?`)) {
        return;
    }

    try {
        const response = await fetch(`${API_BASE}/api/containers/${containerId}`, {
            method: 'DELETE'
        });
        const data = await response.json();

        if (data.success) {
            console.log(`Container ${containerId} terminated`);
            refreshContainers();
        } else {
            alert(`Failed to terminate container: ${data.error}`);
        }
    } catch (error) {
        alert(`Error terminating container: ${error.message}`);
    }
}

function openConsole(containerId) {
    currentContainerId = containerId;
    const modal = document.getElementById('console-modal');
    const output = document.getElementById('console-output');
    const containerIdSpan = document.getElementById('console-container-id');

    containerIdSpan.textContent = containerId;
    output.innerHTML = '<div class="console-line">Connecting to container...</div>';
    modal.classList.add('active');

    // Close existing WebSocket if any
    if (currentConsoleWs) {
        currentConsoleWs.close();
    }

    // Establish WebSocket connection
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/containers/${containerId}/stdio`;

    currentConsoleWs = new WebSocket(wsUrl);

    currentConsoleWs.onopen = () => {
        appendToConsole('Connected to container stdio', 'console-exit');
    };

    currentConsoleWs.onmessage = (event) => {
        try {
            const data = JSON.parse(event.data);
            handleConsoleMessage(data);
        } catch (error) {
            console.error('Failed to parse WebSocket message:', error);
        }
    };

    currentConsoleWs.onerror = (error) => {
        appendToConsole(`WebSocket error: ${error.message || 'Connection failed'}`, 'console-error');
    };

    currentConsoleWs.onclose = () => {
        appendToConsole('Connection closed', 'console-exit');
        currentConsoleWs = null;
    };

    // Focus input
    document.getElementById('console-input').focus();
}

function closeConsole() {
    const modal = document.getElementById('console-modal');
    modal.classList.remove('active');

    if (currentConsoleWs) {
        currentConsoleWs.close();
        currentConsoleWs = null;
    }

    currentContainerId = null;
}

function handleConsoleMessage(data) {
    switch (data.type) {
        case 'container:stdout':
            appendToConsole(data.data.data, 'console-stdout');
            break;
        case 'container:stderr':
            appendToConsole(data.data.data, 'console-stderr');
            break;
        case 'container:exit':
            appendToConsole(`\n[Container exited with code: ${data.data.code}]`, 'console-exit');
            if (currentConsoleWs) {
                currentConsoleWs.close();
            }
            break;
        case 'error':
            const errorMsg = data.data?.message || data.error || 'Unknown error';
            appendToConsole(`\n[ERROR] ${errorMsg}`, 'console-error');
            break;
        case 'info':
            if (data.data && data.data.message) {
                appendToConsole(`[INFO] ${data.data.message}`, 'console-info');
            }
            break;
        case 'debug':
            if (data.data && data.data.message) {
                appendToConsole(`[DEBUG] ${data.data.message}`, 'console-debug');
            }
            break;
        case 'warning':
            if (data.data && data.data.message) {
                appendToConsole(`[WARNING] ${data.data.message}`, 'console-warning');
            }
            break;
        case 'container:name':
            if (data.data && data.data.name) {
                appendToConsole(`[Container] Docker name: ${data.data.name}`, 'console-info');
            }
            break;
        case 'message':
            // Generic raw message - try to extract useful info
            if (data.data) {
                const msg = JSON.stringify(data.data);
                appendToConsole(`[MSG] ${msg}`, 'console-info');
            }
            break;
    }
}

function appendToConsole(text, className = 'console-stdout') {
    const output = document.getElementById('console-output');
    const line = document.createElement('div');
    line.className = `console-line ${className}`;
    line.textContent = text;
    output.appendChild(line);

    // Auto-scroll to bottom
    output.scrollTop = output.scrollHeight;
}

function sendCommand() {
    const input = document.getElementById('console-input');
    const command = input.value;

    if (!command.trim() || !currentConsoleWs) {
        return;
    }

    // Send command as JSON with stdin field
    currentConsoleWs.send(JSON.stringify({ stdin: command }));

    // Echo the command in console
    appendToConsole(`$ ${command}`, 'console-stdout');

    // Clear input
    input.value = '';
}

// Keyboard shortcut to close modal
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
        closeConsole();
    }
});

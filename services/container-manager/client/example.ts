/**
 * Example usage of the Container Manager TypeScript client
 */

import { credentials } from '@grpc/grpc-js';
import { ContainerManagerClient } from './proto/container_manager';

// Create a client instance
const client = new ContainerManagerClient(
  'localhost:50051',  // Server address
  credentials.createInsecure(),  // Use insecure credentials for local development
  {}  // Optional client options
);

// Example 1: List containers
client.listContainers({ filter: 'all' }, (error, response) => {
  if (error) {
    console.error('Error listing containers:', error);
    return;
  }
  console.log('Containers:', response?.containers);
});

// Example 2: Run a container with streaming
const stream = client.run();

// Send create request
stream.write({
  create: {
    config: {
      imageSpec: {
        registry: 'registry-1.docker.io',  // Optional, defaults to Docker Hub
        image: 'alpine:latest',
        // Optional authentication for private registries
        // basicAuth: {
        //   username: 'myuser',
        //   password: 'mypassword',
        // },
      },
      command: ['echo'],  // Override the entrypoint
      args: ['Hello from TypeScript!'],  // Arguments to the command
      env: {},
      cleanup: true,
    },
  },
});

// Example 2b: Using args without overriding command (uses image's default ENTRYPOINT)
// stream.write({
//   create: {
//     config: {
//       imageSpec: { image: 'alpine:latest' },
//       args: ['echo', 'Using default entrypoint'],  // Passed to image's default ENTRYPOINT
//       env: {},
//       cleanup: true,
//     },
//   },
// });

// CRITICAL: Start heartbeat immediately after create
// Client MUST send heartbeat every 30 seconds or container will be terminated
const heartbeatInterval = setInterval(() => {
  try {
    stream.write({ heartbeat: true });
  } catch (err) {
    // Stream closed, stop heartbeat
    clearInterval(heartbeatInterval);
  }
}, 15000); // Send every 15 seconds (safe margin before 30s timeout)

// Handle responses
stream.on('data', (response) => {
  if (response.stdout) {
    process.stdout.write(response.stdout);
  }
  if (response.stderr) {
    process.stderr.write(response.stderr);
  }
  if (response.message) {
    console.log('[MESSAGE]', response.message);
  }
  if (response.exit) {
    console.log('[EXIT]', response.exit.exitCode);
    clearInterval(heartbeatInterval); // Stop heartbeat after container exits
  }
  if (response.error) {
    console.error('[ERROR]', response.error);
    clearInterval(heartbeatInterval); // Stop heartbeat on error
  }
});

stream.on('end', () => {
  console.log('Stream ended');
  clearInterval(heartbeatInterval); // Stop heartbeat when stream ends
});

stream.on('error', (error) => {
  console.error('Stream error:', error);
  clearInterval(heartbeatInterval); // Stop heartbeat on error
});

// Example 3: Check health
client.health({}, (error, response) => {
  if (error) {
    console.error('Health check failed:', error);
    return;
  }
  console.log('Server is healthy:', response?.healthy);
});

// Example 4: Get node resources
client.getNodeResources({}, (error, response) => {
  if (error) {
    console.error('Error getting node resources:', error);
    return;
  }
  console.log('Node resources:', response?.resources);
});

export { client };

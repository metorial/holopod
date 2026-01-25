package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// containerStream manages a persistent Run() stream for a container
type containerStream struct {
	containerID      string
	stream           pb.ContainerManager_RunClient
	cancel           context.CancelFunc
	stdout           []string
	stderr           []string
	messages         []string
	exitCode         *int32
	exitCh           chan int32
	stdoutBroadcast  chan string
	stderrBroadcast  chan string
	messageBroadcast chan string
	mu               sync.RWMutex
}

type Server struct {
	grpcAddr string
	client   pb.ContainerManagerClient
	upgrader websocket.Upgrader

	// Connection management
	streams   map[string]*containerStream
	streamsMu sync.RWMutex
}

func NewServer(grpcAddr string) (*Server, error) {
	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	client := pb.NewContainerManagerClient(conn)

	return &Server{
		grpcAddr: grpcAddr,
		client:   client,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		streams: make(map[string]*containerStream),
	}, nil
}

type Response struct {
	Success     bool    `json:"success"`
	ContainerID *string `json:"container_id,omitempty"`
	Error       *string `json:"error,omitempty"`
}

type CreateContainerRequest struct {
	ContainerID *string           `json:"container_id,omitempty"`
	Image       string            `json:"image"`
	Command     []string          `json:"command,omitempty"`
	Workdir     *string           `json:"workdir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	CPULimit    *string           `json:"cpu_limit,omitempty"`
	MemoryLimit *string           `json:"memory_limit,omitempty"`
	TimeoutSecs *uint32           `json:"timeout_secs,omitempty"`
	Cleanup     *bool             `json:"cleanup,omitempty"`
}

// HandleCreateContainer creates a container and maintains the stream connection
func (s *Server) HandleCreateContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("invalid request body"),
		})
		return
	}

	if req.Image == "" {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("image is required"),
		})
		return
	}

	// Open Run stream
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := s.client.Run(ctx)
	if err != nil {
		cancel()
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String(fmt.Sprintf("failed to open stream: %v", err)),
		})
		return
	}

	// Send create request
	cleanup := true
	if req.Cleanup != nil {
		cleanup = *req.Cleanup
	}

	createReq := &pb.RunRequest{
		Request: &pb.RunRequest_Create{
			Create: &pb.CreateContainer{
				ContainerId: req.ContainerID,
				Config: &pb.ContainerConfig{
					ImageSpec: &pb.ImageSpec{
						Image: req.Image,
					},
					Command: req.Command,
					Workdir: req.Workdir,
					Env:     req.Env,
					Resources: &pb.ResourceLimits{
						CpuLimit:    req.CPULimit,
						MemoryLimit: req.MemoryLimit,
					},
					TimeoutSecs: req.TimeoutSecs,
					Cleanup:     &cleanup,
				},
			},
		},
	}

	if err := stream.Send(createReq); err != nil {
		cancel()
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String(fmt.Sprintf("failed to create container: %v", err)),
		})
		return
	}

	// Wait for created event
	resp, err := stream.Recv()
	if err != nil {
		cancel()
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String(fmt.Sprintf("failed to receive created event: %v", err)),
		})
		return
	}

	_, ok := resp.Event.(*pb.RunResponse_Created)
	if !ok {
		cancel()
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("expected created event"),
		})
		return
	}

	containerID := resp.ContainerId

	// Create stream manager
	cs := &containerStream{
		containerID:      containerID,
		stream:           stream,
		cancel:           cancel,
		exitCh:           make(chan int32, 1),
		stdoutBroadcast:  make(chan string, 100),
		stderrBroadcast:  make(chan string, 100),
		messageBroadcast: make(chan string, 100),
	}

	// Store stream
	s.streamsMu.Lock()
	s.streams[containerID] = cs
	s.streamsMu.Unlock()

	// Start background goroutine to manage stream
	go s.manageStream(cs)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Success:     true,
		ContainerID: proto.String(containerID),
		Error:       nil,
	})
}

// manageStream handles the stream lifecycle
func (s *Server) manageStream(cs *containerStream) {
	defer func() {
		cs.cancel()
		close(cs.stdoutBroadcast)
		close(cs.stderrBroadcast)
		close(cs.messageBroadcast)
		s.streamsMu.Lock()
		delete(s.streams, cs.containerID)
		s.streamsMu.Unlock()
	}()

	for {
		resp, err := cs.stream.Recv()
		if err != nil {
			if err != io.EOF {
				// Stream error
			}
			return
		}

		cs.mu.Lock()
		switch event := resp.Event.(type) {
		case *pb.RunResponse_Stdout:
			data := string(event.Stdout)
			cs.stdout = append(cs.stdout, data)
			// Broadcast to subscribers
			select {
			case cs.stdoutBroadcast <- data:
			default:
			}
		case *pb.RunResponse_Stderr:
			data := string(event.Stderr)
			cs.stderr = append(cs.stderr, data)
			// Broadcast to subscribers
			select {
			case cs.stderrBroadcast <- data:
			default:
			}
		case *pb.RunResponse_Message:
			cs.messages = append(cs.messages, event.Message)
			// Broadcast to subscribers
			select {
			case cs.messageBroadcast <- event.Message:
			default:
			}
		case *pb.RunResponse_Exit:
			cs.exitCode = &event.Exit.ExitCode
			select {
			case cs.exitCh <- event.Exit.ExitCode:
			default:
			}
			cs.mu.Unlock()
			return
		}
		cs.mu.Unlock()
	}
}

// HandleTerminateContainer terminates a container by sending terminate message on stream
func (s *Server) HandleTerminateContainer(w http.ResponseWriter, r *http.Request, containerID string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if containerID == "" {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("container_id is required"),
		})
		return
	}

	force := r.URL.Query().Get("force") == "true"

	s.streamsMu.RLock()
	cs, exists := s.streams[containerID]
	s.streamsMu.RUnlock()

	if !exists {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("container not found or already terminated"),
		})
		return
	}

	// Send terminate request
	terminateReq := &pb.RunRequest{
		Request: &pb.RunRequest_Terminate{
			Terminate: &pb.TerminateContainer{
				Force:       force,
				TimeoutSecs: 5,
			},
		},
	}

	if err := cs.stream.Send(terminateReq); err != nil {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String(fmt.Sprintf("failed to send terminate: %v", err)),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Success: true,
		Error:   nil,
	})
}

// HandleWaitContainer waits for container to exit
func (s *Server) HandleWaitContainer(w http.ResponseWriter, r *http.Request, containerID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if containerID == "" {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("container_id is required"),
		})
		return
	}

	s.streamsMu.RLock()
	cs, exists := s.streams[containerID]
	s.streamsMu.RUnlock()

	if !exists {
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("container not found"),
		})
		return
	}

	// Wait for exit with timeout
	timeout := 120 * time.Second
	if timeoutStr := r.URL.Query().Get("timeout"); timeoutStr != "" {
		if t, err := time.ParseDuration(timeoutStr + "s"); err == nil {
			timeout = t
		}
	}

	select {
	case exitCode := <-cs.exitCh:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success":   true,
			"exit_code": exitCode,
		})
	case <-time.After(timeout):
		json.NewEncoder(w).Encode(Response{
			Success: false,
			Error:   proto.String("timeout waiting for container"),
		})
	}
}

// HandleGetLogs returns buffered logs
func (s *Server) HandleGetLogs(w http.ResponseWriter, r *http.Request, containerID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if containerID == "" {
		http.Error(w, "container_id is required", http.StatusBadRequest)
		return
	}

	s.streamsMu.RLock()
	cs, exists := s.streams[containerID]
	s.streamsMu.RUnlock()

	if !exists {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}

	cs.mu.RLock()
	stdout := make([]string, len(cs.stdout))
	stderr := make([]string, len(cs.stderr))
	messages := make([]string, len(cs.messages))
	copy(stdout, cs.stdout)
	copy(stderr, cs.stderr)
	copy(messages, cs.messages)
	cs.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"stdout":   stdout,
		"stderr":   stderr,
		"messages": messages,
	})
}

func (s *Server) HandleListContainers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filter := r.URL.Query().Get("filter")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := s.client.ListContainers(ctx, &pb.ListContainersRequest{
		Filter: proto.String(filter),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) HandleGetContainer(w http.ResponseWriter, r *http.Request, containerID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := s.client.GetContainerStatus(ctx, &pb.GetContainerStatusRequest{
		ContainerId: containerID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := s.client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type WebSocketMessage struct {
	Type  string  `json:"type"`
	Data  any     `json:"data,omitempty"`
	Stdin *string `json:"stdin,omitempty"`
}

type ContainerConfig struct {
	Image       string            `json:"image"`
	Command     []string          `json:"command,omitempty"`
	Workdir     *string           `json:"workdir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	CPULimit    *string           `json:"cpu_limit,omitempty"`
	MemoryLimit *string           `json:"memory_limit,omitempty"`
	TimeoutSecs *uint32           `json:"timeout_secs,omitempty"`
	Cleanup     *bool             `json:"cleanup,omitempty"`
}

// HandleWebSocket handles interactive WebSocket sessions (existing containers or new ones)
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request, containerID string) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Check if container already exists
	s.streamsMu.RLock()
	cs, exists := s.streams[containerID]
	s.streamsMu.RUnlock()

	if !exists {
		conn.WriteJSON(WebSocketMessage{
			Type: "error",
			Data: map[string]string{"message": "container not found"},
		})
		return
	}

	errCh := make(chan error, 2)

	// Goroutine to read from WebSocket and forward stdin
	go func() {
		for {
			var msg WebSocketMessage
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}

			if msg.Stdin != nil {
				stdinReq := &pb.RunRequest{
					Request: &pb.RunRequest_Stdin{
						Stdin: []byte(*msg.Stdin + "\n"),
					},
				}
				if err := cs.stream.Send(stdinReq); err != nil {
					errCh <- err
					return
				}
			} else if msg.Type == "close_stdin" {
				closeReq := &pb.RunRequest{
					Request: &pb.RunRequest_CloseStdin{
						CloseStdin: true,
					},
				}
				if err := cs.stream.Send(closeReq); err != nil {
					errCh <- err
					return
				}
			} else if msg.Type == "terminate" {
				force := false
				if data, ok := msg.Data.(map[string]any); ok {
					if f, ok := data["force"].(bool); ok {
						force = f
					}
				}
				terminateReq := &pb.RunRequest{
					Request: &pb.RunRequest_Terminate{
						Terminate: &pb.TerminateContainer{
							Force:       force,
							TimeoutSecs: 5,
						},
					},
				}
				if err := cs.stream.Send(terminateReq); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	// Goroutine to forward real-time output to WebSocket
	// Note: Buffered/historical output is available via GET /logs endpoint
	go func() {
		// Stream real-time output only
		for {
			select {
			case line, ok := <-cs.stdoutBroadcast:
				if !ok {
					// Channel closed, stream ended
					return
				}
				if err := conn.WriteJSON(WebSocketMessage{
					Type: "container:stdout",
					Data: map[string]string{"data": line},
				}); err != nil {
					errCh <- err
					return
				}
			case line, ok := <-cs.stderrBroadcast:
				if !ok {
					// Channel closed, stream ended
					return
				}
				if err := conn.WriteJSON(WebSocketMessage{
					Type: "container:stderr",
					Data: map[string]string{"data": line},
				}); err != nil {
					errCh <- err
					return
				}
			case msg, ok := <-cs.messageBroadcast:
				if !ok {
					// Channel closed, stream ended
					return
				}
				var rawData map[string]any
				if err := json.Unmarshal([]byte(msg), &rawData); err == nil {
					if err := conn.WriteJSON(WebSocketMessage{
						Type: "message",
						Data: rawData,
					}); err != nil {
						errCh <- err
						return
					}
				}
			case exitCode := <-cs.exitCh:
				conn.WriteJSON(WebSocketMessage{
					Type: "container:exit",
					Data: map[string]any{
						"exit_code": exitCode,
					},
				})
				errCh <- nil
				return
			}
		}
	}()

	<-errCh
}

// HandleWebSocketRun handles creating a new container via WebSocket
func (s *Server) HandleWebSocketRun(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Read container config from first websocket message
	var firstMsg struct {
		Type   string          `json:"type"`
		Config ContainerConfig `json:"config"`
	}
	if err := conn.ReadJSON(&firstMsg); err != nil {
		conn.WriteJSON(WebSocketMessage{
			Type: "error",
			Data: map[string]string{"message": "failed to read config: " + err.Error()},
		})
		return
	}

	if firstMsg.Type != "create" {
		conn.WriteJSON(WebSocketMessage{
			Type: "error",
			Data: map[string]string{"message": "first message must be type 'create' with config"},
		})
		return
	}

	if firstMsg.Config.Image == "" {
		conn.WriteJSON(WebSocketMessage{
			Type: "error",
			Data: map[string]string{"message": "image is required"},
		})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open unified Run stream
	stream, err := s.client.Run(ctx)
	if err != nil {
		conn.WriteJSON(WebSocketMessage{
			Type: "error",
			Data: map[string]string{"message": err.Error()},
		})
		return
	}

	// Send CreateContainer as first message
	cleanup := true
	if firstMsg.Config.Cleanup != nil {
		cleanup = *firstMsg.Config.Cleanup
	}

	createReq := &pb.RunRequest{
		Request: &pb.RunRequest_Create{
			Create: &pb.CreateContainer{
				Config: &pb.ContainerConfig{
					ImageSpec: &pb.ImageSpec{
						Image: firstMsg.Config.Image,
					},
					Command: firstMsg.Config.Command,
					Workdir: firstMsg.Config.Workdir,
					Env:     firstMsg.Config.Env,
					Resources: &pb.ResourceLimits{
						CpuLimit:    firstMsg.Config.CPULimit,
						MemoryLimit: firstMsg.Config.MemoryLimit,
					},
					TimeoutSecs: firstMsg.Config.TimeoutSecs,
					Cleanup:     &cleanup,
				},
			},
		},
	}

	if err := stream.Send(createReq); err != nil {
		conn.WriteJSON(WebSocketMessage{
			Type: "error",
			Data: map[string]string{"message": "failed to create container: " + err.Error()},
		})
		return
	}

	errCh := make(chan error, 2)

	// Goroutine to read from WebSocket and forward stdin to stream
	go func() {
		for {
			var msg WebSocketMessage
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}

			if msg.Stdin != nil {
				stdinReq := &pb.RunRequest{
					Request: &pb.RunRequest_Stdin{
						Stdin: []byte(*msg.Stdin + "\n"),
					},
				}
				if err := stream.Send(stdinReq); err != nil {
					errCh <- err
					return
				}
			} else if msg.Type == "close_stdin" {
				closeReq := &pb.RunRequest{
					Request: &pb.RunRequest_CloseStdin{
						CloseStdin: true,
					},
				}
				if err := stream.Send(closeReq); err != nil {
					errCh <- err
					return
				}
			} else if msg.Type == "terminate" {
				force := false
				if data, ok := msg.Data.(map[string]any); ok {
					if f, ok := data["force"].(bool); ok {
						force = f
					}
				}
				terminateReq := &pb.RunRequest{
					Request: &pb.RunRequest_Terminate{
						Terminate: &pb.TerminateContainer{
							Force:       force,
							TimeoutSecs: 5,
						},
					},
				}
				if err := stream.Send(terminateReq); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	// Goroutine to read from stream and forward to WebSocket
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					errCh <- nil
					return
				}
				errCh <- err
				return
			}

			var wsMsg WebSocketMessage

			switch event := resp.Event.(type) {
			case *pb.RunResponse_Created:
				wsMsg = WebSocketMessage{
					Type: "container:created",
					Data: map[string]string{
						"container_id": resp.ContainerId,
						"state":        event.Created.State.String(),
					},
				}

			case *pb.RunResponse_Stdout:
				wsMsg = WebSocketMessage{
					Type: "container:stdout",
					Data: map[string]string{"data": string(event.Stdout)},
				}

			case *pb.RunResponse_Stderr:
				wsMsg = WebSocketMessage{
					Type: "container:stderr",
					Data: map[string]string{"data": string(event.Stderr)},
				}

			case *pb.RunResponse_Exit:
				wsMsg = WebSocketMessage{
					Type: "container:exit",
					Data: map[string]any{
						"exit_code": event.Exit.ExitCode,
						"timestamp": event.Exit.Timestamp,
					},
				}

			case *pb.RunResponse_Error:
				wsMsg = WebSocketMessage{
					Type: "container:error",
					Data: map[string]string{"message": event.Error},
				}

			case *pb.RunResponse_Message:
				var rawData map[string]any
				if err := json.Unmarshal([]byte(event.Message), &rawData); err == nil {
					wsMsg = WebSocketMessage{
						Type: "message",
						Data: rawData,
					}
				} else {
					continue
				}

			default:
				continue
			}

			if err := conn.WriteJSON(wsMsg); err != nil {
				errCh <- err
				return
			}

			// If we received an exit event, we're done
			if _, isExit := resp.Event.(*pb.RunResponse_Exit); isExit {
				errCh <- nil
				return
			}
		}
	}()

	// Wait for either goroutine to finish
	<-errCh

	// Close the stream gracefully
	stream.CloseSend()
}

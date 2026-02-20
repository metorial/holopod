package publicapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Server struct {
	client   pb.ContainerManagerClient
	upgrader websocket.Upgrader
}

func NewServer(grpcAddr string) (*Server, error) {
	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	return &Server{
		client: pb.NewContainerManagerClient(conn),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}, nil
}

type IncomingMessage struct {
	Type        string          `json:"type"`
	Create      *CreateEnvelope `json:"create,omitempty"`
	Stdin       *string         `json:"stdin,omitempty"`
	Force       *bool           `json:"force,omitempty"`
	TimeoutSecs *uint32         `json:"timeoutSecs,omitempty"`
}

type CreateEnvelope struct {
	ContainerID *string         `json:"containerId,omitempty"`
	Config      ContainerConfig `json:"config"`
}

type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ImageSpec struct {
	Registry  *string    `json:"registry,omitempty"`
	Image     string     `json:"image"`
	BasicAuth *BasicAuth `json:"basicAuth,omitempty"`
}

type ResourceLimits struct {
	CPULimit    *string `json:"cpuLimit,omitempty"`
	MemoryLimit *string `json:"memoryLimit,omitempty"`
}

type NetworkRule struct {
	Action         string  `json:"action"`
	Protocol       *string `json:"protocol,omitempty"`
	Destination    *string `json:"destination,omitempty"`
	PortRangeStart *uint32 `json:"portRangeStart,omitempty"`
	PortRangeEnd   *uint32 `json:"portRangeEnd,omitempty"`
}

type NetworkConfig struct {
	Rules         []NetworkRule `json:"rules,omitempty"`
	DefaultPolicy *string       `json:"defaultPolicy,omitempty"`
	DNSServers    []string      `json:"dnsServers,omitempty"`
}

type ContainerConfig struct {
	ImageSpec   ImageSpec         `json:"imageSpec"`
	Command     []string          `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Workdir     *string           `json:"workdir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Resources   *ResourceLimits   `json:"resources,omitempty"`
	Network     *NetworkConfig    `json:"network,omitempty"`
	TimeoutSecs *uint32           `json:"timeoutSecs,omitempty"`
	Cleanup     *bool             `json:"cleanup,omitempty"`
}

func (c ContainerConfig) toProto() (*pb.ContainerConfig, error) {
	if c.ImageSpec.Image == "" {
		return nil, fmt.Errorf("config.imageSpec.image is required")
	}

	cleanup := true
	if c.Cleanup != nil {
		cleanup = *c.Cleanup
	}

	imageSpec := &pb.ImageSpec{
		Registry: c.ImageSpec.Registry,
		Image:    c.ImageSpec.Image,
	}
	if c.ImageSpec.BasicAuth != nil {
		imageSpec.Auth = &pb.ImageSpec_BasicAuth{
			BasicAuth: &pb.BasicAuth{
				Username: c.ImageSpec.BasicAuth.Username,
				Password: c.ImageSpec.BasicAuth.Password,
			},
		}
	}

	var resources *pb.ResourceLimits
	if c.Resources != nil {
		resources = &pb.ResourceLimits{
			CpuLimit:    c.Resources.CPULimit,
			MemoryLimit: c.Resources.MemoryLimit,
		}
	}

	var network *pb.NetworkConfig
	if c.Network != nil {
		rules := make([]*pb.NetworkRule, 0, len(c.Network.Rules))
		for _, rule := range c.Network.Rules {
			rules = append(rules, &pb.NetworkRule{
				Action:         rule.Action,
				Protocol:       rule.Protocol,
				Destination:    rule.Destination,
				PortRangeStart: rule.PortRangeStart,
				PortRangeEnd:   rule.PortRangeEnd,
			})
		}
		network = &pb.NetworkConfig{
			Rules:         rules,
			DefaultPolicy: c.Network.DefaultPolicy,
			DnsServers:    c.Network.DNSServers,
		}
	}

	return &pb.ContainerConfig{
		ImageSpec:   imageSpec,
		Command:     c.Command,
		Args:        c.Args,
		Workdir:     c.Workdir,
		Env:         c.Env,
		Resources:   resources,
		Network:     network,
		TimeoutSecs: c.TimeoutSecs,
		Cleanup:     &cleanup,
	}, nil
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) HandleRun(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	var first IncomingMessage
	if err := conn.ReadJSON(&first); err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "failed to read first message"})
		return
	}
	if first.Type != "create" || first.Create == nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "first message must be create"})
		return
	}

	config, err := first.Create.Config.toProto()
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := s.client.Run(ctx)
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}

	if err := stream.Send(&pb.RunRequest{
		Request: &pb.RunRequest_Create{
			Create: &pb.CreateContainer{
				ContainerId: first.Create.ContainerID,
				Config:      config,
			},
		},
	}); err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}

	errCh := make(chan error, 2)

	go func() {
		for {
			var msg IncomingMessage
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}

			switch msg.Type {
			case "stdin":
				if msg.Stdin == nil {
					continue
				}
				if err := stream.Send(&pb.RunRequest{
					Request: &pb.RunRequest_Stdin{Stdin: []byte(*msg.Stdin)},
				}); err != nil {
					errCh <- err
					return
				}
			case "close_stdin":
				if err := stream.Send(&pb.RunRequest{
					Request: &pb.RunRequest_CloseStdin{CloseStdin: true},
				}); err != nil {
					errCh <- err
					return
				}
			case "terminate":
				force := false
				if msg.Force != nil {
					force = *msg.Force
				}
				timeoutSecs := uint32(5)
				if msg.TimeoutSecs != nil {
					timeoutSecs = *msg.TimeoutSecs
				}
				if err := stream.Send(&pb.RunRequest{
					Request: &pb.RunRequest_Terminate{
						Terminate: &pb.TerminateContainer{
							Force:       force,
							TimeoutSecs: timeoutSecs,
						},
					},
				}); err != nil {
					errCh <- err
					return
				}
			case "heartbeat":
				if err := stream.Send(&pb.RunRequest{
					Request: &pb.RunRequest_Heartbeat{Heartbeat: true},
				}); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

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

			switch event := resp.Event.(type) {
			case *pb.RunResponse_Created:
				err = conn.WriteJSON(map[string]any{
					"type":        "created",
					"containerId": resp.ContainerId,
					"state":       event.Created.State.String(),
				})
			case *pb.RunResponse_Stdout:
				err = conn.WriteJSON(map[string]any{
					"type": "stdout",
					"data": string(event.Stdout),
				})
			case *pb.RunResponse_Stderr:
				err = conn.WriteJSON(map[string]any{
					"type": "stderr",
					"data": string(event.Stderr),
				})
			case *pb.RunResponse_Message:
				var message any
				if unmarshalErr := json.Unmarshal([]byte(event.Message), &message); unmarshalErr != nil {
					message = event.Message
				}
				err = conn.WriteJSON(map[string]any{
					"type": "message",
					"data": message,
				})
			case *pb.RunResponse_Error:
				err = conn.WriteJSON(map[string]any{
					"type":  "error",
					"error": event.Error,
				})
			case *pb.RunResponse_Exit:
				err = conn.WriteJSON(map[string]any{
					"type":      "exit",
					"exitCode":  event.Exit.ExitCode,
					"timestamp": event.Exit.Timestamp,
				})
				if err == nil {
					errCh <- nil
					return
				}
			default:
				continue
			}

			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	<-errCh
	_ = stream.CloseSend()
}

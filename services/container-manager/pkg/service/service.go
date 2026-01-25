package service

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/metorial/fleet/holopod/services/container-manager/pkg/manager"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Service struct {
	pb.UnimplementedContainerManagerServer
	manager *manager.Manager
}

func New(mgr *manager.Manager) *Service {
	return &Service{
		manager: mgr,
	}
}

// Run implements the unified bidirectional stream for container lifecycle
// CRITICAL: Connection close/interrupt automatically terminates container
// CRITICAL: Client MUST send heartbeat every 30 seconds or container will be terminated
func (s *Service) Run(stream pb.ContainerManager_RunServer) error {
	var containerID string
	var cleanupDone bool

	// CRITICAL: Ensure container is ALWAYS terminated when stream ends
	defer func() {
		if containerID != "" && !cleanupDone {
			// Force terminate on connection drop
			_ = s.manager.TerminateContainer(containerID, true, 5)
			cleanupDone = true
		}
	}()

	// First message MUST be create request
	firstMsg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive initial message: %v", err)
	}

	createReq := firstMsg.GetCreate()
	if createReq == nil {
		return status.Errorf(codes.InvalidArgument, "first message must be CreateContainer request")
	}

	// Validate config
	if createReq.Config == nil {
		return status.Errorf(codes.InvalidArgument, "config is required")
	}

	if createReq.Config.ImageSpec == nil {
		return status.Errorf(codes.InvalidArgument, "image_spec is required")
	}

	if createReq.Config.ImageSpec.Image == "" {
		return status.Errorf(codes.InvalidArgument, "image is required")
	}

	// Generate or use provided container ID
	if createReq.ContainerId != nil {
		containerID = *createReq.ContainerId
	}

	// Create and start container
	id, err := s.manager.CreateContainer(stream.Context(), containerID, createReq.Config)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create container: %v", err)
	}
	containerID = id

	// Send created event
	if err := stream.Send(&pb.RunResponse{
		ContainerId: containerID,
		Event: &pb.RunResponse_Created{
			Created: &pb.ContainerCreated{
				ContainerId: containerID,
				State:       pb.ContainerState_RUNNING,
			},
		},
	}); err != nil {
		return err
	}

	// Subscribe to container output
	stdoutCh := s.manager.SubscribeStdout(containerID)
	stderrCh := s.manager.SubscribeStderr(containerID)
	msgCh := s.manager.SubscribeMessages(containerID)

	// Channel for receiving stdin from client
	stdinCh := make(chan []byte, 10)
	errCh := make(chan error, 2)

	// Heartbeat tracking - client MUST send heartbeat every 30 seconds
	lastHeartbeat := time.Now()
	heartbeatCh := make(chan struct{}, 1)

	// Goroutine to monitor heartbeat timeout
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Check if heartbeat timeout exceeded
				if time.Since(lastHeartbeat) > 30*time.Second {
					errCh <- status.Errorf(codes.DeadlineExceeded, "heartbeat timeout: no heartbeat received for 30 seconds")
					return
				}
			case <-heartbeatCh:
				lastHeartbeat = time.Now()
			case <-stream.Context().Done():
				return
			}
		}
	}()

	// Goroutine to receive messages from client
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					// Client closed their send stream
					errCh <- nil
					return
				}
				errCh <- err
				return
			}

			// Handle different request types
			if stdin := msg.GetStdin(); stdin != nil {
				stdinCh <- stdin
			} else if msg.GetCloseStdin() {
				close(stdinCh)
			} else if msg.GetHeartbeat() {
				// Update heartbeat timestamp
				select {
				case heartbeatCh <- struct{}{}:
				default:
				}
			} else if terminate := msg.GetTerminate(); terminate != nil {
				// Client requested termination
				force := terminate.Force
				timeout := terminate.TimeoutSecs
				if timeout == 0 {
					timeout = 5
				}
				if err := s.manager.TerminateContainer(containerID, force, timeout); err != nil {
					errCh <- err
					return
				}
				cleanupDone = true
				errCh <- nil
				return
			}
		}
	}()

	// Goroutine to forward stdin to container
	go func() {
		for data := range stdinCh {
			if err := s.manager.WriteStdin(containerID, data); err != nil {
				// Ignore stdin errors - container may have exited
				continue
			}
		}
	}()

	// Main event loop - forward container output to client
	for {
		select {
		case data, ok := <-stdoutCh:
			if !ok {
				// Channel closed, container exited
				goto done
			}
			if err := stream.Send(&pb.RunResponse{
				ContainerId: containerID,
				Event: &pb.RunResponse_Stdout{
					Stdout: data,
				},
			}); err != nil {
				return err
			}

		case data, ok := <-stderrCh:
			if !ok {
				goto done
			}
			if err := stream.Send(&pb.RunResponse{
				ContainerId: containerID,
				Event: &pb.RunResponse_Stderr{
					Stderr: data,
				},
			}); err != nil {
				return err
			}

		case msg, ok := <-msgCh:
			if !ok {
				goto done
			}
			if err := stream.Send(&pb.RunResponse{
				ContainerId: containerID,
				Event: &pb.RunResponse_Message{
					Message: msg,
				},
			}); err != nil {
				return err
			}

		case err := <-errCh:
			if err != nil {
				return err
			}
			// Client closed connection, terminate container
			goto done

		case <-stream.Context().Done():
			// Context cancelled (connection dropped)
			goto done
		}
	}

done:
	// Wait for container exit and send exit event
	exitCode, err := s.manager.WaitContainer(containerID, 10)
	if err == nil {
		_ = stream.Send(&pb.RunResponse{
			ContainerId: containerID,
			Event: &pb.RunResponse_Exit{
				Exit: &pb.ContainerExit{
					ExitCode:  exitCode,
					Timestamp: fmt.Sprintf("%d", time.Now().Unix()),
				},
			},
		})
	}

	cleanupDone = true
	return nil
}

func (s *Service) ListContainers(ctx context.Context, req *pb.ListContainersRequest) (*pb.ListContainersResponse, error) {
	filter := "all"
	if req.Filter != nil {
		filter = *req.Filter
	}

	containers := s.manager.ListContainers(filter)

	return &pb.ListContainersResponse{
		Containers: containers,
	}, nil
}

func (s *Service) GetContainerStatus(ctx context.Context, req *pb.GetContainerStatusRequest) (*pb.GetContainerStatusResponse, error) {
	if req.ContainerId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "container_id is required")
	}

	containerStatus, err := s.manager.GetContainerStatus(req.ContainerId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "container not found: %v", err)
	}

	return &pb.GetContainerStatusResponse{
		Success: true,
		Status:  containerStatus,
	}, nil
}

func (s *Service) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	totalContainers, runningContainers := s.manager.GetStats()

	return &pb.HealthResponse{
		Healthy:           true,
		Version:           "1.0.0",
		RunningContainers: uint32(runningContainers),
		TotalContainers:   uint32(totalContainers),
		HealthIssues:      []string{},
	}, nil
}

func (s *Service) GetNodeResources(ctx context.Context, req *pb.GetNodeResourcesRequest) (*pb.GetNodeResourcesResponse, error) {
	totalContainers, runningContainers := s.manager.GetStats()

	cpuCount := runtime.NumCPU()

	// Try to get memory info
	var totalMemory, availableMemory, usedMemory uint64

	// Get disk info from /tmp
	var diskTotal, diskAvailable, diskUsed uint64

	return &pb.GetNodeResourcesResponse{
		Success: true,
		Resources: &pb.NodeResources{
			CpuCores:             uint32(cpuCount),
			MemoryTotalBytes:     totalMemory,
			MemoryAvailableBytes: availableMemory,
			MemoryUsedBytes:      usedMemory,
			DiskTotalBytes:       diskTotal,
			DiskAvailableBytes:   diskAvailable,
			DiskUsedBytes:        diskUsed,
			RunningContainers:    uint32(runningContainers),
			TotalContainers:      uint32(totalContainers),
		},
	}, nil
}

func (s *Service) GetAvailableImages(ctx context.Context, req *pb.GetAvailableImagesRequest) (*pb.GetAvailableImagesResponse, error) {
	cmd := exec.Command("docker", "images", "--format", "{{.ID}}|{{.Repository}}:{{.Tag}}|{{.Size}}|{{.CreatedAt}}")
	output, err := cmd.Output()
	if err != nil {
		return &pb.GetAvailableImagesResponse{
			Success: false,
			Error:   proto.String(fmt.Sprintf("failed to list images: %v", err)),
			Images:  []*pb.ImageInfo{},
		}, nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	images := make([]*pb.ImageInfo, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}

		imageID := parts[0]
		repoTag := parts[1]
		// size := parts[2] // Not parsed, just for display
		created := parts[3]

		images = append(images, &pb.ImageInfo{
			Id:       imageID,
			RepoTags: []string{repoTag},
			Created:  created,
		})
	}

	return &pb.GetAvailableImagesResponse{
		Success: true,
		Images:  images,
	}, nil
}

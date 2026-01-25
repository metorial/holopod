package service

import (
	"context"
	"os"
	"testing"

	"github.com/metorial/fleet/holopod/services/container-manager/pkg/manager"
	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func setupTestService(t *testing.T) (*Service, *manager.Manager) {
	os.Setenv("ISOLATION_RUNNER_PATH", "/tmp/fake-runner")
	t.Cleanup(func() {
		os.Unsetenv("ISOLATION_RUNNER_PATH")
	})

	mgr, err := manager.New()
	if err != nil {
		t.Skipf("Cannot create manager: %v", err)
		return nil, nil
	}
	t.Cleanup(func() {
		mgr.Stop()
	})

	svc := New(mgr)
	return svc, mgr
}

func TestNew(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	if svc.manager == nil {
		t.Error("Service manager should not be nil")
	}
}

// Note: Run() RPC tests would require a mock streaming implementation
// Testing bidirectional streams is complex and requires integration tests
// The integration tests in test_integration.sh cover the Run() stream

func TestListContainersEmpty(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	req := &pb.ListContainersRequest{}

	resp, err := svc.ListContainers(context.Background(), req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Response should not be nil")
	}
	if len(resp.Containers) != 0 {
		t.Errorf("Expected 0 containers, got %d", len(resp.Containers))
	}
}

func TestListContainersWithFilter(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	filter := "running"
	req := &pb.ListContainersRequest{
		Filter: &filter,
	}

	resp, err := svc.ListContainers(context.Background(), req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Response should not be nil")
	}
}

func TestGetContainerStatusMissingID(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	req := &pb.GetContainerStatusRequest{
		ContainerId: "",
	}

	_, err := svc.GetContainerStatus(context.Background(), req)
	if err == nil {
		t.Error("Expected error for missing container_id")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Error("Expected gRPC status error")
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument, got %v", st.Code())
	}
}

func TestGetContainerStatusNotFound(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	req := &pb.GetContainerStatusRequest{
		ContainerId: "nonexistent",
	}

	_, err := svc.GetContainerStatus(context.Background(), req)
	if err == nil {
		t.Error("Expected error for nonexistent container")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Error("Expected gRPC status error")
	}
	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound, got %v", st.Code())
	}
}

func TestHealth(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	req := &pb.HealthRequest{}

	resp, err := svc.Health(context.Background(), req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Response should not be nil")
	}
	if !resp.Healthy {
		t.Error("Expected healthy status")
	}
	if resp.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", resp.Version)
	}
	if resp.RunningContainers != 0 {
		t.Errorf("Expected 0 running containers, got %d", resp.RunningContainers)
	}
	if resp.TotalContainers != 0 {
		t.Errorf("Expected 0 total containers, got %d", resp.TotalContainers)
	}
}

func TestGetNodeResources(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	req := &pb.GetNodeResourcesRequest{}

	resp, err := svc.GetNodeResources(context.Background(), req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if resp == nil {
		t.Fatal("Response should not be nil")
	}
	if !resp.Success {
		t.Error("Expected success")
	}
	if resp.Resources == nil {
		t.Fatal("Resources should not be nil")
	}
	if resp.Resources.CpuCores == 0 {
		t.Error("CPU count should be > 0")
	}
}

func TestGetAvailableImages(t *testing.T) {
	svc, _ := setupTestService(t)
	if svc == nil {
		return
	}

	req := &pb.GetAvailableImagesRequest{}

	resp, err := svc.GetAvailableImages(context.Background(), req)
	// May fail if docker is not available, that's ok
	if err == nil {
		if resp == nil {
			t.Fatal("Response should not be nil")
		}
		if !resp.Success {
			t.Error("Expected success when docker is available")
		}
		if resp.Images == nil {
			t.Error("Images array should not be nil")
		}
	}
}

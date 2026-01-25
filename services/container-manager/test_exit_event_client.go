package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	pb "github.com/metorial/fleet/holopod/services/container-manager/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Connect to container-manager
	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewContainerManagerClient(conn)

	fmt.Println("Test 1: Container with invalid command")
	fmt.Println("==========================================")
	testInvalidCommand(client)

	fmt.Println("\n\nTest 2: Container with invalid image")
	fmt.Println("==========================================")
	testInvalidImage(client)
}

func testInvalidCommand(client pb.ContainerManagerClient) {
	ctx := context.Background()

	// Create stream
	stream, err := client.Run(ctx)
	if err != nil {
		log.Fatalf("Failed to create stream: %v", err)
	}

	// Send create request with invalid command
	req := &pb.RunRequest{
		Request: &pb.RunRequest_Create{
			Create: &pb.CreateContainer{
				Config: &pb.ContainerConfig{
					ImageSpec: &pb.ImageSpec{
						Image: "alpine:latest",
					},
					Command: []string{"nonexistent_command_xyz"},
					Cleanup: func() *bool { b := true; return &b }(),
				},
			},
		},
	}

	if err := stream.Send(req); err != nil {
		log.Fatalf("Failed to send request: %v", err)
	}

	// Close send to signal we're done sending
	if err := stream.CloseSend(); err != nil {
		log.Fatalf("Failed to close send: %v", err)
	}

	// Receive and display all messages
	foundExitEvent := false
	messageCount := 0
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			fmt.Println("Stream closed (EOF)")
			break
		}
		if err != nil {
			log.Printf("Stream error: %v", err)
			break
		}

		messageCount++

		fmt.Printf("[DEBUG] Message %d - ContainerID: %s\n", messageCount, resp.ContainerId)

		// Check which field is set in the response
		switch {
		case resp.GetCreated() != nil:
			fmt.Printf("  Event: Container created (ID: %s)\n", resp.ContainerId)
		case len(resp.GetStdout()) > 0:
			fmt.Printf("  Stdout: %s\n", string(resp.GetStdout()))
		case len(resp.GetStderr()) > 0:
			fmt.Printf("  Stderr: %s\n", string(resp.GetStderr()))
		case resp.GetExit() != nil:
			fmt.Printf("  Event: Container exited (code: %d)\n", resp.GetExit().ExitCode)
		case resp.GetError() != "":
			fmt.Printf("  Error: %s\n", resp.GetError())
		case resp.GetMessage() != "":
			fmt.Printf("  Message (length: %d): ", len(resp.GetMessage()))
			// Parse message as JSON to check if it's a structured event
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(resp.GetMessage()), &msg); err == nil {
				// Pretty print the message
				prettyMsg, _ := json.MarshalIndent(msg, "", "  ")
				fmt.Printf("\n%s\n", prettyMsg)

				// Check if this is a container_exited event
				if msgType, ok := msg["type"].(string); ok && msgType == "container_exited" {
					foundExitEvent = true
				}
			} else {
				fmt.Printf("(Non-JSON) %q\n", resp.GetMessage())
			}
		default:
			fmt.Printf("  [WARNING] No field set in response!\n")
			// Debug: print all fields
			fmt.Printf("    Created: %v\n", resp.GetCreated())
			fmt.Printf("    Stdout len: %d\n", len(resp.GetStdout()))
			fmt.Printf("    Stderr len: %d\n", len(resp.GetStderr()))
			fmt.Printf("    Exit: %v\n", resp.GetExit())
			fmt.Printf("    Error: %q\n", resp.GetError())
			fmt.Printf("    Message: %q\n", resp.GetMessage())
		}
	}

	fmt.Printf("\nTotal messages received: %d\n", messageCount)

	if foundExitEvent {
		fmt.Println("\n✓ SUCCESS: Found container_exited structured event!")
	} else {
		fmt.Println("\n✗ FAILURE: No container_exited structured event found!")
	}
}

func testInvalidImage(client pb.ContainerManagerClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create stream
	stream, err := client.Run(ctx)
	if err != nil {
		log.Fatalf("Failed to create stream: %v", err)
	}

	// Send create request with invalid image
	req := &pb.RunRequest{
		Request: &pb.RunRequest_Create{
			Create: &pb.CreateContainer{
				Config: &pb.ContainerConfig{
					ImageSpec: &pb.ImageSpec{
						Image: "this-image-does-not-exist-xyz:latest",
					},
					Command: []string{"echo", "hello"},
					Cleanup: func() *bool { b := true; return &b }(),
				},
			},
		},
	}

	if err := stream.Send(req); err != nil {
		log.Fatalf("Failed to send request: %v", err)
	}

	// Close send to signal we're done sending
	if err := stream.CloseSend(); err != nil {
		log.Fatalf("Failed to close send: %v", err)
	}

	// Receive and display all messages
	foundExitEvent := false
	messageCount := 0
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			fmt.Println("Stream closed (EOF)")
			break
		}
		if err != nil {
			log.Printf("Stream error: %v", err)
			break
		}

		messageCount++

		fmt.Printf("[DEBUG] Message %d - ContainerID: %s\n", messageCount, resp.ContainerId)

		// Check which field is set in the response
		switch {
		case resp.GetCreated() != nil:
			fmt.Printf("  Event: Container created (ID: %s)\n", resp.ContainerId)
		case len(resp.GetStdout()) > 0:
			fmt.Printf("  Stdout: %s\n", string(resp.GetStdout()))
		case len(resp.GetStderr()) > 0:
			fmt.Printf("  Stderr: %s\n", string(resp.GetStderr()))
		case resp.GetExit() != nil:
			fmt.Printf("  Event: Container exited (code: %d)\n", resp.GetExit().ExitCode)
		case resp.GetError() != "":
			fmt.Printf("  Error: %s\n", resp.GetError())
		case resp.GetMessage() != "":
			fmt.Printf("  Message (length: %d): ", len(resp.GetMessage()))
			// Parse message as JSON to check if it's a structured event
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(resp.GetMessage()), &msg); err == nil {
				// Pretty print the message
				prettyMsg, _ := json.MarshalIndent(msg, "", "  ")
				fmt.Printf("\n%s\n", prettyMsg)

				// Check if this is a container_exited event
				if msgType, ok := msg["type"].(string); ok && msgType == "container_exited" {
					foundExitEvent = true
				}
			} else {
				fmt.Printf("(Non-JSON) %q\n", resp.GetMessage())
			}
		default:
			fmt.Printf("  [WARNING] No field set in response!\n")
			// Debug: print all fields
			fmt.Printf("    Created: %v\n", resp.GetCreated())
			fmt.Printf("    Stdout len: %d\n", len(resp.GetStdout()))
			fmt.Printf("    Stderr len: %d\n", len(resp.GetStderr()))
			fmt.Printf("    Exit: %v\n", resp.GetExit())
			fmt.Printf("    Error: %q\n", resp.GetError())
			fmt.Printf("    Message: %q\n", resp.GetMessage())
		}
	}

	fmt.Printf("\nTotal messages received: %d\n", messageCount)

	if foundExitEvent {
		fmt.Println("\n✓ SUCCESS: Found container_exited structured event!")
	} else {
		fmt.Println("\n✗ FAILURE: No container_exited structured event found!")
	}
}

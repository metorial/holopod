package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

const (
	// Containers older than this are considered orphaned
	maxAge = 24 * time.Hour
)

func main() {
	ctx := context.Background()

	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer docker.Close()

	// Find all containers managed by isolation-runner
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "managed-by=isolation-runner")

	containers, err := docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		log.Fatalf("Failed to list containers: %v", err)
	}

	if len(containers) == 0 {
		fmt.Println("No isolation-runner containers found")
		return
	}

	fmt.Printf("Found %d isolation-runner containers\n", len(containers))

	cleaned := 0
	errors := 0

	for _, c := range containers {
		containerName := "unknown"
		if name, ok := c.Labels["container-name"]; ok {
			containerName = name
		}

		// Check creation timestamp
		if ts, ok := c.Labels["creation-timestamp"]; ok {
			timestamp, err := strconv.ParseInt(ts, 10, 64)
			if err == nil {
				age := time.Since(time.Unix(timestamp, 0))

				// Check if container is still running
				isRunning := c.State == "running"

				fmt.Printf("\nContainer: %s\n", containerName)
				fmt.Printf("  ID: %s\n", c.ID[:12])
				fmt.Printf("  State: %s\n", c.State)
				fmt.Printf("  Age: %s\n", age.Round(time.Second))

				// Only clean up stopped containers or very old running containers
				shouldClean := false
				reason := ""

				if !isRunning {
					shouldClean = true
					reason = "container has exited"
				} else if age > maxAge {
					shouldClean = true
					reason = fmt.Sprintf("container is older than %s", maxAge)
				}

				if shouldClean {
					fmt.Printf("  Action: Cleaning up (%s)\n", reason)

					// Stop container if running
					if isRunning {
						timeout := 5
						if err := docker.ContainerStop(ctx, c.ID, container.StopOptions{
							Timeout: &timeout,
						}); err != nil {
							fmt.Printf("  Error stopping: %v\n", err)
							errors++
							continue
						}
						fmt.Println("  Stopped container")
					}

					// Remove container
					if err := docker.ContainerRemove(ctx, c.ID, container.RemoveOptions{
						Force: true,
					}); err != nil {
						fmt.Printf("  Error removing: %v\n", err)
						errors++
						continue
					}

					fmt.Println("  Removed container")
					cleaned++
				} else {
					fmt.Println("  Action: Keeping (container is still running and recent)")
				}
			}
		} else {
			fmt.Printf("\nContainer: %s (no timestamp)\n", containerName)
			fmt.Printf("  ID: %s\n", c.ID[:12])
			fmt.Printf("  State: %s\n", c.State)

			if c.State != "running" {
				fmt.Println("  Action: Cleaning up (exited, no timestamp)")

				if err := docker.ContainerRemove(ctx, c.ID, container.RemoveOptions{
					Force: true,
				}); err != nil {
					fmt.Printf("  Error removing: %v\n", err)
					errors++
					continue
				}

				fmt.Println("  Removed container")
				cleaned++
			}
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total containers found: %d\n", len(containers))
	fmt.Printf("Cleaned up: %d\n", cleaned)
	fmt.Printf("Errors: %d\n", errors)

	if errors > 0 {
		os.Exit(1)
	}
}

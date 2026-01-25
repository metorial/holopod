package container

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/metorial/fleet/holopod/internal/isolation-runner/pkg/jsonmsg"
)

type StdinMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func (m *Manager) StartStdinForwarder(ctx context.Context) error {
	if m.containerID == "" {
		return fmt.Errorf("container not created")
	}

	if !m.config.Execution.AttachStdin {
		return nil
	}

	resp, err := m.docker.ContainerAttach(ctx, m.containerID, container.AttachOptions{
		Stream: true,
		Stdin:  true,
	})
	if err != nil {
		return fmt.Errorf("failed to attach stdin to container: %w", err)
	}

	go func() {
		defer resp.Close()

		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()

			var msg StdinMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}

			if msg.Type != "stdin" {
				continue
			}

			data, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				jsonmsg.Warning(fmt.Sprintf("Failed to decode stdin data: %v", err))
				continue
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				jsonmsg.Warning("Stdin write timeout")
				return
			default:
				if _, err := resp.Conn.Write(data); err != nil {
					if err != io.EOF {
						jsonmsg.Warning(fmt.Sprintf("Failed to write to container stdin: %v", err))
					}
					return
				}
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			jsonmsg.Warning(fmt.Sprintf("Stdin scanner error: %v", err))
		}
	}()

	return nil
}

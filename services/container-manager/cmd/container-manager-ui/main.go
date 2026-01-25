package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/metorial/fleet/holopod/services/container-manager/pkg/api"
)

const version = "1.0.0"

func main() {
	listenAddr := os.Getenv("LISTEN_ADDRESS")
	if listenAddr == "" {
		listenAddr = "0.0.0.0:3000"
	}

	grpcAddr := os.Getenv("CONTAINER_MANAGER_ADDR")
	if grpcAddr == "" {
		grpcAddr = "localhost:50051"
	}

	log.Printf("Container Manager UI v%s starting...", version)
	log.Printf("Listen address: %s", listenAddr)
	log.Printf("gRPC address: %s", grpcAddr)

	server, err := api.NewServer(grpcAddr)
	if err != nil {
		log.Fatalf("Failed to create API server: %v", err)
	}

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/api/health", server.HandleHealth)

	// Container operations
	mux.HandleFunc("/api/containers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			server.HandleCreateContainer(w, r)
		} else if r.Method == http.MethodGet {
			server.HandleListContainers(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Container-specific operations
	mux.HandleFunc("/api/containers/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/containers/")
		parts := strings.Split(path, "/")

		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}

		containerID := parts[0]

		// GET /api/containers/{id} - Get container status
		if len(parts) == 1 {
			if r.Method == http.MethodGet {
				server.HandleGetContainer(w, r, containerID)
			} else if r.Method == http.MethodDelete {
				server.HandleTerminateContainer(w, r, containerID)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		// Sub-resources
		if len(parts) >= 2 {
			switch parts[1] {
			case "wait":
				// GET /api/containers/{id}/wait - Wait for container exit
				server.HandleWaitContainer(w, r, containerID)
			case "logs":
				// GET /api/containers/{id}/logs - Get container logs
				server.HandleGetLogs(w, r, containerID)
			case "stdio":
				// WebSocket /api/containers/{id}/stdio - Interactive I/O
				server.HandleWebSocket(w, r, containerID)
			default:
				http.NotFound(w, r)
			}
			return
		}

		http.NotFound(w, r)
	})

	// WebSocket endpoint for creating new containers with I/O streaming
	mux.HandleFunc("/api/run", server.HandleWebSocketRun)

	staticDir := "./static"
	if envDir := os.Getenv("STATIC_DIR"); envDir != "" {
		staticDir = envDir
	}
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: corsMiddleware(mux),
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal, stopping...")
		httpServer.Close()
	}()

	log.Printf("Container Manager UI server listening on %s", listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to serve: %v", err)
	}

	log.Println("Container Manager UI stopped")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

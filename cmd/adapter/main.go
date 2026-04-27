package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentic-layer/guardrail-adapter/internal/extproc"
	"github.com/agentic-layer/guardrail-adapter/internal/metadata"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	// Parse command-line flags
	addr := flag.String("addr", ":9001", "Address to listen on (format: host:port)")
	healthAddr := flag.String("health-addr", ":8080", "Health check HTTP server address")
	flag.Parse()

	// Static config path (optional). When set, the adapter ignores dynamic
	// metadata and x-guardrail-* headers entirely.
	cfgPath := os.Getenv("GUARDRAIL_CONFIG_FILE")
	var staticCfg *metadata.GuardrailConfig
	if cfgPath != "" {
		loaded, err := metadata.LoadGuardrailConfigFile(cfgPath)
		if err != nil {
			log.Fatalf("failed to load static config file %s: %v", cfgPath, err)
		}
		staticCfg = loaded
		log.Printf("static config loaded from %s: provider=%s modes=%v", cfgPath, loaded.Provider, loaded.Modes)
	} else {
		log.Printf("static config disabled; using dynamic metadata/headers")
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Register ext_proc service
	extprocServer := extproc.NewServer(staticCfg)
	extprocv3.RegisterExternalProcessorServer(grpcServer, extprocServer)

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register gRPC server reflection for introspection tools (e.g. grpcurl)
	reflection.Register(grpcServer)

	// Start gRPC server
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// Start HTTP health check server
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "OK\n")
	})
	healthHTTPServer := &http.Server{
		Addr:    *healthAddr,
		Handler: healthMux,
	}

	go func() {
		log.Printf("Health check server listening on %s", *healthAddr)
		if err := healthHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("health check server failed: %v", err)
		}
	}()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("shutting down gracefully...")
		grpcServer.GracefulStop()
		_ = healthHTTPServer.Close()
	}()

	// Start serving
	log.Printf("ext_proc server listening on %s", *addr)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

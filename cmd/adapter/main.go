package main

import (
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentic-layer/guardrail-adapter/internal/extproc"
	"github.com/agentic-layer/guardrail-adapter/internal/logging"
	"github.com/agentic-layer/guardrail-adapter/internal/metadata"
	"github.com/dustin/go-humanize"
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
	maxBodySize := flag.String("max-body-size", "1MiB",
		"Maximum buffered body size per direction (e.g. 512KiB, 2MiB). "+
			"Bodies that exceed this in the inspection path are rejected with "+
			"HTTP 413 (request) or 502 (response). Pass-through traffic is unaffected.")
	flag.Parse()

	maxBodyBytes, err := humanize.ParseBytes(*maxBodySize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --max-body-size %q: %v\n", *maxBodySize, err)
		os.Exit(2)
	}

	// Build the application logger from LOG_LEVEL / LOG_FORMAT env vars.
	// Invalid values warn and fall back to defaults so deploys are not broken
	// by a typo.
	logger, logErr := logging.New()
	if logErr != nil {
		fmt.Fprintln(os.Stderr, logErr)
	}
	slog.SetDefault(logger)

	// Static config path (optional). When set, the adapter ignores dynamic
	// metadata and x-guardrail-* headers entirely.
	cfgPath := os.Getenv("GUARDRAIL_CONFIG_FILE")
	var staticCfg *metadata.GuardrailConfig
	if cfgPath != "" {
		loaded, err := metadata.LoadGuardrailConfigFile(cfgPath)
		if err != nil {
			slog.Error("failed to load static config", "path", cfgPath, "error", err)
			os.Exit(1)
		}
		staticCfg = loaded
		slog.Info("static config loaded",
			"path", cfgPath,
			"provider", loaded.Provider,
			"modes", loaded.Modes,
		)
	} else {
		slog.Info("static config disabled, using dynamic metadata/headers")
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Register ext_proc service
	if maxBodyBytes > math.MaxInt64 {
		fmt.Fprintf(os.Stderr, "--max-body-size %q exceeds max int64\n", *maxBodySize)
		os.Exit(2)
	}
	extprocServer := extproc.NewServer(logger, staticCfg, int64(maxBodyBytes))
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
		slog.Error("failed to listen", "addr", *addr, "error", err)
		os.Exit(1)
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
		slog.Info("health check server listening", "addr", *healthAddr)
		if err := healthHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health check server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("shutting down gracefully")
		grpcServer.GracefulStop()
		_ = healthHTTPServer.Close()
	}()

	// Start serving
	slog.Info("ext_proc server listening", "addr", *addr)
	if err := grpcServer.Serve(listener); err != nil {
		slog.Error("failed to serve", "error", err)
		os.Exit(1)
	}
}

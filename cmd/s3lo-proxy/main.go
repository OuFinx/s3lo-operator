package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OuFinx/s3lo-operator/pkg/proxy"
	"github.com/OuFinx/s3lo-operator/pkg/setup"
	s3client "github.com/finx/s3lo/pkg/s3"
)

func main() {
	port := envOr("S3LO_PORT", "5732")
	certsDir := envOr("S3LO_CERTS_DIR", "/etc/containerd/certs.d")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Write containerd hosts.toml
	if err := setup.WriteHostsConfig(certsDir, port); err != nil {
		log.Fatalf("Failed to write containerd config: %v", err)
	}
	log.Printf("Wrote containerd hosts config to %s/s3/hosts.toml", certsDir)

	// Create S3 client
	client, err := s3client.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}

	// Start proxy
	srv := proxy.NewServer(client, port)

	go func() {
		log.Printf("Starting s3lo-proxy on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	log.Println("Shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

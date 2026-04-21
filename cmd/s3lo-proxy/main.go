package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/OuFinx/s3lo-operator/pkg/proxy"
	"github.com/OuFinx/s3lo-operator/pkg/setup"
	"github.com/OuFinx/s3lo/pkg/storage"
)

func main() {
	port := envOr("S3LO_PORT", "5732")
	certsDir := envOr("S3LO_CERTS_DIR", "/etc/containerd/certs.d")

	presignTTL, err := time.ParseDuration(envOr("S3LO_PRESIGN_TTL", "1h"))
	if err != nil {
		log.Fatalf("Invalid S3LO_PRESIGN_TTL: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := setup.WriteHostsConfig(certsDir, port); err != nil {
		log.Fatalf("Failed to write containerd config: %v", err)
	}
	log.Printf("Wrote containerd hosts config to %s/s3/hosts.toml", certsDir)

	client, err := storage.NewS3Client(ctx)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}

	var verifier *proxy.Verifier
	if os.Getenv("S3LO_VERIFY_SIGNATURES") == "true" {
		keyRef := os.Getenv("S3LO_KEY_REF")
		if keyRef == "" {
			log.Fatal("S3LO_KEY_REF must be set when S3LO_VERIFY_SIGNATURES=true")
		}
		v, err := proxy.NewVerifier(ctx, keyRef)
		if err != nil {
			log.Fatalf("Failed to load signing key %q: %v", keyRef, err)
		}
		verifier = v
		log.Printf("Signature verification enabled (key: %s)", keyRef)
	}

	cacheMaxEntries := 10000
	if v := os.Getenv("S3LO_CACHE_MAX_ENTRIES"); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 {
			cacheMaxEntries = n
		}
	}
	cacheTTL, err2 := time.ParseDuration(envOr("S3LO_CACHE_TTL", "24h"))
	if err2 != nil {
		log.Fatalf("Invalid S3LO_CACHE_TTL: %v", err2)
	}

	srv := proxy.NewServer(client, proxy.ServerConfig{
		Port:            port,
		PresignTTL:      presignTTL,
		CacheMaxEntries: cacheMaxEntries,
		CacheDir:        os.Getenv("S3LO_CACHE_DIR"),
		CacheTTL:        cacheTTL,
		Verifier:        verifier,
	})

	go func() {
		log.Printf("Starting s3lo-proxy on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

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

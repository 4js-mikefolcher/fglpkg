package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/registry/server"
)

func main() {
	var (
		addr     = flag.String("addr", env("REGISTRY_ADDR", ":8080"), "Address to listen on")
		dataDir  = flag.String("data", env("REGISTRY_DATA_DIR", "./registry-data"), "Metadata storage directory")
		baseURL  = flag.String("base-url", env("REGISTRY_BASE_URL", ""), "Public base URL of this registry")
		readAuth = flag.Bool("require-read-auth", envBool("REGISTRY_REQUIRE_READ_AUTH"), "Require auth for read routes")
	)
	flag.Parse()

	publishToken := env("FGLPKG_PUBLISH_TOKEN", "")
	if publishToken == "" {
		log.Fatal("FGLPKG_PUBLISH_TOKEN is not set. Generate one with: openssl rand -hex 32")
	}

	cfg := server.Config{
		Addr:            *addr,
		DataDir:         *dataDir,
		PublishToken:    publishToken,
		BaseURL:         *baseURL,
		RequireReadAuth: *readAuth,
		R2: server.R2Config{
			AccountID:       env("R2_ACCOUNT_ID", ""),
			AccessKeyID:     env("R2_ACCESS_KEY_ID", ""),
			AccessKeySecret: env("R2_ACCESS_KEY_SECRET", ""),
			BucketName:      env("R2_BUCKET_NAME", ""),
			PublicBucketURL: env("R2_PUBLIC_BUCKET_URL", ""),
		},
	}

	storageMode := "local filesystem"
	if cfg.R2.AccountID != "" {
		storageMode = fmt.Sprintf("Cloudflare R2 (bucket: %s)", cfg.R2.BucketName)
	}
	readMode := "public (no read auth)"
	if cfg.RequireReadAuth {
		readMode = "authenticated reads required"
	}
	log.Printf("fglpkg registry")
	log.Printf("  listening:   %s", cfg.Addr)
	log.Printf("  data dir:    %s", cfg.DataDir)
	log.Printf("  storage:     %s", storageMode)
	log.Printf("  read access: %s", readMode)
	if cfg.BaseURL != "" {
		log.Printf("  base URL:    %s", cfg.BaseURL)
	}
	if err := server.Run(cfg); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" { return v }
	return fallback
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "true" || v == "1" || v == "yes"
}

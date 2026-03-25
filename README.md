# fglpkg - Genero BDL Package Manager

A package manager for Genero BDL with BDL packages, Java JAR dependencies,
Genero version constraints, workspaces, and a self-hosted registry backed
by Cloudflare R2.

## Quick Start

    go build -o fglpkg ./cmd/fglpkg
    sudo mv fglpkg /usr/local/bin/
    echo 'eval "$(fglpkg env)"' >> ~/.bashrc
    source ~/.bashrc
    fglpkg init
    fglpkg install

## Registry Server

    go build -o fglpkg-registry ./cmd/registry
    # See scripts/setup-fly.sh for full Fly.io deployment

## Project Layout

    fglpkg/
    cmd/fglpkg/        - CLI entry point
    cmd/registry/      - Registry server entry point
    internal/
      cli/             - Command implementations
      manifest/        - fglpkg.json
      semver/          - Version parsing and constraint matching
      genero/          - Genero BDL version detection
      resolver/        - Transitive dependency resolution
      installer/       - Download, verify, extract
      lockfile/        - fglpkg.lock
      checksum/        - SHA256 verification
      workspace/       - Monorepo support
      credentials/     - ~/.fglpkg/credentials.json
      env/             - FGLLDPATH generation
      registry/        - HTTP client + server
        server/        - Handlers, store, auth, blob
    scripts/setup-fly.sh
    Dockerfile
    fly.toml
    .github/workflows/

## Environment Variables

  FGLPKG_HOME              Override ~/.fglpkg
  FGLPKG_REGISTRY          Registry URL
  FGLPKG_PUBLISH_TOKEN     Admin/publish token
  FGLPKG_GENERO_VERSION    Override Genero version detection
  R2_ACCOUNT_ID            Cloudflare account ID
  R2_ACCESS_KEY_ID         R2 API key ID
  R2_ACCESS_KEY_SECRET     R2 API secret
  R2_BUCKET_NAME           R2 bucket name
  R2_PUBLIC_BUCKET_URL     Public CDN URL for the R2 bucket

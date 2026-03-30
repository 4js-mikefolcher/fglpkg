# fglpkg — Genero BDL Package Manager

A package manager for Genero BDL projects, supporting both BDL packages and Java JAR dependencies.

## Project Structure

```
fglpkg/
├── cmd/
│   ├── fglpkg/main.go              # Package manager CLI entry point
│   ├── registry/main.go            # Registry server entry point
│   └── build.sh                    # Cross-platform build script
├── internal/
│   ├── cli/cli.go                  # Command dispatch & user interaction
│   ├── manifest/manifest.go        # fglpkg.json parsing & manipulation
│   ├── semver/                     # Semver parsing & constraint matching
│   ├── genero/genero.go            # Genero BDL version detection
│   ├── resolver/resolver.go        # Transitive dependency resolution
│   ├── installer/installer.go      # Zip download, extraction, JAR management
│   ├── lockfile/lockfile.go        # fglpkg.lock read/write/validate
│   ├── checksum/checksum.go        # SHA256 streaming verification
│   ├── credentials/                # Registry auth credential storage
│   ├── workspace/workspace.go      # Monorepo workspace support
│   ├── registry/registry.go        # Registry HTTP client
│   └── registry/server/            # Registry HTTP server
│       ├── server.go               # Route handlers
│       ├── store.go                # Flat-file storage backend
│       └── testing.go              # Test helper (NewTestServer)
├── .github/workflows/release.yml   # Automated release on tag push
├── go.mod
└── README.md
```

## Installation

Download the latest binary for your platform from [GitHub Releases](https://github.com/4js-mikefolcher/fglpkg/releases) and place it in your `PATH`:

```bash
# Example for macOS Apple Silicon
sudo cp fglpkg-darwin-arm64 /usr/local/bin/fglpkg
sudo chmod +x /usr/local/bin/fglpkg
```

Add environment setup to your shell profile:

```bash
echo 'eval "$(fglpkg env)"' >> ~/.bashrc
source ~/.bashrc
```

## Building from Source

```bash
go build -o fglpkg ./cmd/fglpkg
```

Use the build script to cross-compile for all platforms with embedded version info:

```bash
FGLPKG_VERSION=1.0.0 ./cmd/build.sh
```

This produces ARM and Intel binaries for Linux, macOS, and Windows in the `./bin/` directory.

## Home Directory Layout

fglpkg stores everything under `~/.fglpkg` (override with `FGLPKG_HOME`):

```
~/.fglpkg/
├── packages/          # Installed BDL packages (each in its own subdir)
│   ├── myutils/
│   │   ├── fglpkg.json
│   │   ├── strings.42m
│   │   └── dates.42m
│   └── poiapi/
│       └── com/fourjs/poiapi/
│           ├── fglpkg.json
│           └── PoiApi.42m
├── jars/              # Java JARs
│   ├── gson-2.10.1.jar
│   └── commons-lang3-3.12.0.jar
└── credentials.json   # Registry auth tokens
```

## fglpkg.json Format

### For a project (consuming packages)

```json
{
  "name": "myproject",
  "version": "1.0.0",
  "description": "My Genero BDL project",
  "author": "Jane Developer",
  "license": "MIT",
  "dependencies": {
    "fgl": {
      "myutils": "^1.0.0",
      "dbtools": "2.1.0"
    },
    "java": [
      {
        "groupId": "com.google.code.gson",
        "artifactId": "gson",
        "version": "2.10.1"
      }
    ]
  }
}
```

### For a package (publishing to registry)

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "description": "POI API for Genero BDL",
  "author": "Jane Developer",
  "license": "MIT",
  "root": "com/fourjs/poiapi",
  "genero": "^4.0.0",
  "main": "PoiApi.42m",
  "dependencies": {
    "java": [
      {
        "groupId": "org.apache.poi",
        "artifactId": "poi",
        "version": "5.2.3"
      }
    ]
  }
}
```

### Manifest Fields

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Package name (used as the registry identifier) |
| `version` | Yes | Semver version string |
| `description` | No | Short description |
| `author` | No | Author name |
| `license` | No | License identifier (e.g., `MIT`, `Apache-2.0`) |
| `repository` | No | Source repository URL |
| `main` | No | Primary `.42m` entry point |
| `genero` | No | Genero BDL version constraint (e.g., `^4.0.0`) |
| `root` | No | Base directory for package files when publishing (default `.`) |
| `files` | No | Glob patterns for files to include in the zip (default `["*.42m", "*.42f", "*.sch"]`) |
| `dependencies.fgl` | No | BDL package dependencies (`name` -> `version constraint`) |
| `dependencies.java` | No | Java JAR dependencies (Maven coordinates) |
| `scripts` | No | Custom script definitions |

## Environment Variables

| Variable | Purpose |
|---|---|
| `FGLPKG_HOME` | Override default `~/.fglpkg` home |
| `FGLPKG_REGISTRY` | Override default registry URL |
| `FGLPKG_PUBLISH_TOKEN` | Admin/publish token (bypasses credentials file) |
| `FGLPKG_GITHUB_TOKEN` | GitHub PAT for package uploads/downloads (private repo) |
| `FGLPKG_GITHUB_REPO` | GitHub `owner/repo` for package storage (e.g., `4js-mikefolcher/fglpkg-packages`) |
| `FGLPKG_GENERO_VERSION` | Override Genero version detection |
| `FGLLDPATH` | Auto-managed by `fglpkg env` (prepends, preserves existing value) |
| `CLASSPATH` | Auto-managed by `fglpkg env` (prepends, preserves existing value) |

## Usage

```bash
fglpkg init                   # Initialise fglpkg.json interactively
fglpkg install                # Install all deps from fglpkg.json
fglpkg install myutils        # Add + install latest version
fglpkg install myutils@1.2.0  # Add + install specific version
fglpkg remove myutils         # Remove a package
fglpkg update                 # Re-resolve and update all dependencies
fglpkg list                   # List installed packages
fglpkg env                    # Print export statements
fglpkg search json            # Search registry
fglpkg publish                # Publish current package to registry
fglpkg unpublish pkg@1.0.0    # Remove a published version
fglpkg login                  # Save registry credentials
fglpkg logout                 # Remove saved credentials
fglpkg whoami                 # Show current authenticated user
fglpkg config github-repos list          # List configured GitHub repos
fglpkg config github-repos add o/r       # Add a GitHub package repo (admin)
fglpkg config github-repos remove o/r    # Remove a GitHub package repo (admin)
fglpkg owner list <pkg>       # List package owners
fglpkg owner add <pkg> <user> # Add a package owner
fglpkg workspace init         # Initialise a monorepo workspace
fglpkg version                # Print version and build info
fglpkg help                   # Show help
```

## Running the Registry Server

```bash
# Build the registry binary
go build -o fglpkg-registry ./cmd/registry

# Start the server
export FGLPKG_PUBLISH_TOKEN=my-secret-token
./fglpkg-registry \
  --addr :8080 \
  --data /var/lib/fglpkg-registry \
  --base-url https://registry.example.com

# Point fglpkg clients at your registry
export FGLPKG_REGISTRY=https://registry.example.com
```

### Registry API

| Method | Path | Description |
|---|---|---|
| `GET` | `/packages/:name/versions` | List all versions + Genero constraints |
| `GET` | `/packages/:name/:version` | Full package metadata |
| `GET` | `/packages/:name/:version/download` | Download the zip |
| `POST` | `/packages/:name/:version/publish` | Publish a new version (auth required) |
| `DELETE` | `/packages/:name/:version/unpublish` | Remove a published version (auth required) |
| `GET` | `/config` | Registry configuration (GitHub repos) |
| `POST` | `/config/github-repos` | Add a GitHub repo (admin only) |
| `DELETE` | `/config/github-repos/:owner/:repo` | Remove a GitHub repo (admin only) |
| `GET` | `/search?q=<term>` | Search by name or description |
| `GET` | `/health` | Liveness probe |

### Publishing a Package

Package zips are stored as GitHub Release assets on a private repository. The registry server on Fly.io stores only metadata (no zip files).

First, an admin configures the GitHub repo on the registry (one-time setup):

```bash
fglpkg config github-repos add 4js-mikefolcher/fglpkg-packages
```

Then any authenticated user can publish:

```bash
# Log in (prompts for both registry token and GitHub token)
fglpkg login

# Publish
fglpkg publish
```

The CLI automatically fetches the GitHub repo from the registry config. You can override it with `FGLPKG_GITHUB_REPO` if needed.

The publish flow:
1. Builds a zip from the directory specified by `root` (or `.`), collecting files matching `files` patterns (default: `*.42m`, `*.42f`, `*.sch`)
2. Uploads the zip as a GitHub Release asset to the private packages repo
3. Registers metadata (description, checksum, download URL, dependencies) with the registry

**GitHub token requirements:**
- Publishers need a fine-grained PAT with **Contents: Read and write** on the packages repo
- Consumers (installers) need a fine-grained PAT with **Contents: Read** on the packages repo

### Genero Version Variants

Each package version can have multiple builds, one per Genero major version. When you publish, fglpkg detects your local Genero version and tags the upload as a variant:

```bash
# On a Genero 4.x machine
fglpkg publish    # uploads poiapi-1.0.0-genero4.zip

# On a Genero 6.x machine
fglpkg publish    # uploads poiapi-1.0.0-genero6.zip
```

Both variants live under the same release (`poiapi-v1.0.0`) as separate assets. When a consumer runs `fglpkg install`, the resolver automatically selects the variant matching their local Genero major version.

### Registry Storage Layout

```
/var/lib/fglpkg-registry/
├── index.json                  # global package catalogue
└── packages/
    └── myutils/
        ├── meta.json           # all version records for myutils
        ├── 1.0.0.zip
        └── 1.1.0.zip
```

## Releases

Releases are automated via GitHub Actions. Push a tag to create a release with binaries for all platforms:

```bash
git tag v1.0.0
git push origin v1.0.0
```

Pre-built binaries are available at [GitHub Releases](https://github.com/4js-mikefolcher/fglpkg/releases).

# fglpkg — Genero BDL Package Manager

A package manager for Genero BDL projects, supporting both BDL packages and Java JAR dependencies.

## Project Structure

```
fglpkg/
├── cmd/
│   ├── fglpkg/main.go              # Package manager CLI entry point
│   └── registry/main.go            # Registry server entry point
├── internal/
│   ├── cli/cli.go                  # Command dispatch & user interaction
│   ├── manifest/manifest.go        # fglpkg.json parsing & manipulation
│   ├── semver/                     # Semver parsing & constraint matching
│   ├── genero/genero.go            # Genero BDL version detection
│   ├── resolver/resolver.go        # Transitive dependency resolution
│   ├── installer/installer.go      # Zip download, extraction, JAR management
│   ├── lockfile/lockfile.go        # fglpkg.lock read/write/validate
│   ├── checksum/checksum.go        # SHA256 streaming verification
│   ├── workspace/workspace.go      # Monorepo workspace support
│   ├── registry/registry.go        # Registry HTTP client
│   └── registry/server/            # Registry HTTP server
│       ├── server.go               # Route handlers
│       ├── store.go                # Flat-file storage backend
│       └── testing.go              # Test helper (NewTestServer)
├── go.mod
└── README.md
```

## Building

```bash
go build -o fglpkg ./cmd/fglpkg
```

Cross-compile for other platforms:

```bash
GOOS=windows GOARCH=amd64 go build -o fglpkg.exe ./cmd/fglpkg
GOOS=darwin  GOARCH=arm64 go build -o fglpkg-mac  ./cmd/fglpkg
```

## Installation

Copy the binary to a directory in your `PATH`:

```bash
sudo cp fglpkg /usr/local/bin/
```

Add environment setup to your shell profile:

```bash
echo 'eval "$(fglpkg env)"' >> ~/.bashrc
source ~/.bashrc
```

## Home Directory Layout

fglpkg stores everything under `~/.fglpkg` (override with `FGLPKG_HOME`):

```
~/.fglpkg/
├── packages/          # Installed BDL packages (each in its own subdir)
│   ├── myutils/
│   │   ├── fglpkg.json
│   │   ├── strings.42m
│   │   └── dates.42m
│   └── dbtools/
│       └── ...
└── jars/              # Java JARs
    ├── gson-2.10.1.jar
    └── commons-lang3-3.12.0.jar
```

## fglpkg.json Format

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
      },
      {
        "groupId": "org.apache.commons",
        "artifactId": "commons-lang3",
        "version": "3.12.0"
      }
    ]
  }
}
```

## Environment Variables

| Variable | Purpose |
|---|---|
| `FGLPKG_HOME` | Override default `~/.fglpkg` home |
| `FGLPKG_REGISTRY` | Override default registry URL |
| `FGLLDPATH` | Auto-managed by `fglpkg env` |
| `FGLJAVAPROPERTY_java.class.path` | Auto-managed by `fglpkg env` |

## Usage

```bash
fglpkg init                   # Initialise fglpkg.json interactively
fglpkg install                # Install all deps from fglpkg.json
fglpkg install myutils        # Add + install latest version
fglpkg install myutils@1.2.0  # Add + install specific version
fglpkg remove myutils         # Remove a package
fglpkg list                   # List installed packages
fglpkg env                    # Print export statements
fglpkg search json            # Search registry
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
| `GET` | `/search?q=<term>` | Search by name or description |
| `GET` | `/health` | Liveness probe |

### Publishing a Package

```bash
export FGLPKG_PUBLISH_TOKEN=my-secret-token
export FGLPKG_REGISTRY=https://registry.example.com
fglpkg publish
```

`fglpkg publish` zips all `*.42m`, `*.42f`, `*.sch`, and `fglpkg.json` files
in the current directory, computes the SHA256 checksum, and POSTs a multipart
request to the registry.

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

- [ ] Semver constraint resolution (`^1.0.0`, `~2.1`, `>=1.0 <2.0`)
- [ ] Dependency graph / transitive dependency resolution
- [ ] SHA256 checksum verification after download
- [ ] `fglpkg publish` command to publish packages to the registry
- [ ] `fglpkg update` to upgrade to latest compatible versions
- [ ] Lock file (`fglpkg.lock`) for reproducible installs
- [ ] Registry server implementation
- [ ] Workspace support (monorepos)

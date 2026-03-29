# fglpkg User Guide

This guide covers the day-to-day usage of fglpkg, the package manager for Genero BDL projects.

## Table of Contents

- [Getting Started](#getting-started)
- [Installing fglpkg](#installing-fglpkg)
- [Setting Up Your Environment](#setting-up-your-environment)
- [Creating a New Project](#creating-a-new-project)
- [Managing Dependencies](#managing-dependencies)
- [Publishing a Package](#publishing-a-package)
- [Working with Java JARs](#working-with-java-jars)
- [Registry Authentication](#registry-authentication)
- [Workspaces (Monorepos)](#workspaces-monorepos)
- [Lock Files](#lock-files)
- [Package Ownership](#package-ownership)
- [Troubleshooting](#troubleshooting)

---

## Getting Started

fglpkg manages two types of dependencies for your Genero BDL projects:

- **BDL packages** — compiled Genero modules (`.42m`, `.42f`, `.sch` files) published to a registry
- **Java JARs** — Java libraries downloaded from Maven Central (or custom URLs), needed when your BDL code calls into Java

## Installing fglpkg

### Download a Pre-built Binary

Visit the [Releases page](https://github.com/4js-mikefolcher/fglpkg/releases) and download the binary for your platform:

| Platform | Binary |
|---|---|
| Linux (Intel) | `fglpkg-linux-amd64` |
| Linux (ARM) | `fglpkg-linux-arm64` |
| macOS (Apple Silicon) | `fglpkg-darwin-arm64` |
| macOS (Intel) | `fglpkg-darwin-amd64` |
| Windows (Intel) | `fglpkg-windows-amd64.exe` |
| Windows (ARM) | `fglpkg-windows-arm64.exe` |

Place the binary in a directory on your `PATH`:

```bash
# macOS / Linux
sudo cp fglpkg-darwin-arm64 /usr/local/bin/fglpkg
sudo chmod +x /usr/local/bin/fglpkg
```

```powershell
# Windows — copy to a directory in your PATH
copy fglpkg-windows-amd64.exe C:\tools\fglpkg.exe
```

Verify the installation:

```bash
fglpkg version
```

### Build from Source

If you have Go installed:

```bash
git clone https://github.com/4js-mikefolcher/fglpkg.git
cd fglpkg
go build -o fglpkg ./cmd/fglpkg
sudo cp fglpkg /usr/local/bin/
```

## Setting Up Your Environment

fglpkg manages the `FGLLDPATH` and `CLASSPATH` environment variables so Genero can find installed packages and JARs. Add this line to your shell profile (`~/.bashrc`, `~/.zshrc`, or equivalent):

```bash
eval "$(fglpkg env)"
```

Then reload your shell:

```bash
source ~/.bashrc
```

Running `fglpkg env` on its own shows the export statements it generates:

```bash
$ fglpkg env
export FGLLDPATH=/Users/you/.fglpkg/packages/myutils:/Users/you/.fglpkg/packages/poiapi"${FGLLDPATH:+:$FGLLDPATH}"
export CLASSPATH=/Users/you/.fglpkg/jars/poi-5.2.3.jar:/Users/you/.fglpkg/jars/gson-2.10.1.jar"${CLASSPATH:+:$CLASSPATH}"
```

Key points:
- Existing `FGLLDPATH` and `CLASSPATH` values are preserved (fglpkg prepends its paths)
- All installed package directories are added to `FGLLDPATH`
- All downloaded JARs are added to `CLASSPATH`

### Home Directory

Everything fglpkg manages lives under `~/.fglpkg` by default. Override this by setting the `FGLPKG_HOME` environment variable:

```bash
export FGLPKG_HOME=/opt/fglpkg
```

## Creating a New Project

To start a new Genero BDL project with fglpkg:

```bash
mkdir myproject
cd myproject
fglpkg init
```

This interactively prompts for the package name, version, description, and author, then creates a `fglpkg.json` file:

```json
{
  "name": "myproject",
  "version": "0.1.0",
  "description": "",
  "author": "",
  "license": "UNLICENSED",
  "dependencies": {
    "fgl": {},
    "java": []
  }
}
```

## Managing Dependencies

### Installing All Dependencies

If your project already has a `fglpkg.json` with dependencies listed, install them all:

```bash
fglpkg install
```

This resolves the dependency graph, writes a lock file (`fglpkg.lock`), downloads BDL packages from the registry, and downloads Java JARs from Maven Central.

### Adding a Package

To add a BDL package dependency:

```bash
# Add the latest version
fglpkg install myutils

# Add a specific version
fglpkg install myutils@1.2.0
```

This resolves the version, adds it to your `fglpkg.json`, and installs it.

### Removing a Package

```bash
fglpkg remove myutils
```

This deletes the package from `~/.fglpkg/packages/` and removes it from `fglpkg.json`.

### Updating Dependencies

To re-resolve all dependencies to their latest compatible versions (ignoring the lock file):

```bash
fglpkg update
```

### Listing Installed Packages

```bash
$ fglpkg list
Installed packages:
  myutils                        1.0.0
  poiapi                         1.0.0
```

### Searching the Registry

```bash
$ fglpkg search json
Results for "json":
  NAME                           VERSION      DESCRIPTION
  ----                           -------      -----------
  jsonutils                      2.0.1        JSON utility functions for BDL
```

## Publishing a Package

### Package Structure

A publishable package needs a `fglpkg.json` with at least `name` and `version`. The `root` field tells fglpkg where to find the compiled files.

**Example: Simple package (flat directory)**

```
myutils/
├── fglpkg.json
├── strings.42m
└── dates.42m
```

```json
{
  "name": "myutils",
  "version": "1.0.0",
  "description": "String and date utilities for BDL"
}
```

**Example: Fully-qualified package (Java-style directory structure)**

If your package uses a fully-qualified name like `com.fourjs.poiapi`, Genero expects the `.42m` files to live in a matching directory structure. Set `root` to tell fglpkg where to find them:

```
poiapi/
├── fglpkg.json
└── com/
    └── fourjs/
        └── poiapi/
            ├── PoiApi.42m
            └── PoiHelper.42m
```

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "description": "POI API for Genero BDL",
  "root": "com/fourjs/poiapi",
  "genero": "^4.0.0",
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

When published, the zip preserves the full directory structure (`com/fourjs/poiapi/PoiApi.42m`). When installed, it extracts to `~/.fglpkg/packages/poiapi/com/fourjs/poiapi/PoiApi.42m`. Since `~/.fglpkg/packages/poiapi` is on the `FGLLDPATH`, Genero resolves `com.fourjs.poiapi` correctly.

### File Selection

By default, fglpkg collects files matching `*.42m`, `*.42f`, and `*.sch`. To customize this, use the `files` field:

```json
{
  "files": ["*.42m", "*.42f", "*.sch", "*.str"]
}
```

### Publishing

First, authenticate with the registry:

```bash
fglpkg login
```

Then publish:

```bash
fglpkg publish
```

This builds a zip of your package files, computes a SHA256 checksum, and uploads it to the registry.

### Genero Version Constraints

Use the `genero` field to declare which Genero BDL versions your package supports:

```json
{
  "genero": "^4.0.0"
}
```

Supported constraint syntax:
- `^1.0.0` — compatible with 1.x.x (>=1.0.0, <2.0.0)
- `~1.2.0` — patch-level changes (>=1.2.0, <1.3.0)
- `>=3.20.0 <5.0.0` — explicit range
- `^3.20.0 || ^4.0.0` — multiple ranges
- `*` or omit — compatible with any version

## Working with Java JARs

Genero BDL can call Java code, so fglpkg also manages JAR dependencies. Declare them using Maven coordinates:

```json
{
  "dependencies": {
    "java": [
      {
        "groupId": "com.google.code.gson",
        "artifactId": "gson",
        "version": "2.10.1"
      },
      {
        "groupId": "org.apache.poi",
        "artifactId": "poi",
        "version": "5.2.3",
        "checksum": "abc123..."
      }
    ]
  }
}
```

### Optional JAR Fields

| Field | Description |
|---|---|
| `checksum` | SHA256 hex digest for integrity verification (optional, Maven Central is trusted by default) |
| `jar` | Override the JAR filename (default: `artifactId-version.jar`) |
| `url` | Override the download URL entirely (default: Maven Central) |

JARs are downloaded to `~/.fglpkg/jars/` and added to `CLASSPATH` by `fglpkg env`.

## Registry Authentication

### Logging In

```bash
$ fglpkg login
Registry URL (https://registry.fglpkg.dev):
Token: my-secret-token
✓ Logged in to https://registry.fglpkg.dev as jdeveloper
```

Credentials are stored in `~/.fglpkg/credentials.json`.

### Checking Your Identity

```bash
$ fglpkg whoami
Logged in to https://registry.fglpkg.dev as jdeveloper
```

### Logging Out

```bash
fglpkg logout
```

### Using a Token Directly

For CI/CD environments, set the token as an environment variable instead of using `fglpkg login`:

```bash
export FGLPKG_PUBLISH_TOKEN=my-secret-token
fglpkg publish
```

### Token Management (Admin)

Administrators can create, revoke, and rotate tokens:

```bash
# Create a token for a new user
fglpkg token create jdeveloper

# Revoke a user's token
fglpkg token revoke jdeveloper

# Rotate your own token
fglpkg token rotate
```

## Workspaces (Monorepos)

Workspaces let you develop multiple related packages in a single repository. Local packages are automatically linked via `FGLLDPATH` without needing to publish and install them.

### Setting Up a Workspace

```bash
# In your monorepo root
fglpkg workspace init packages/myutils packages/dbtools
```

This creates a `fglpkg-workspace.json` file. Each listed path should contain its own `fglpkg.json`.

### Adding Members

```bash
fglpkg workspace add packages/newlib
```

### Listing Members

```bash
$ fglpkg workspace list
Workspace: /path/to/monorepo
  myutils                        v1.0.0
  dbtools                        v2.1.0
  newlib                         v0.1.0
```

### Workspace Info

```bash
fglpkg workspace info
```

### How It Works

When `fglpkg env` detects that you are inside a workspace, it adds each member's source directory to `FGLLDPATH` with higher priority than installed packages. This means you can edit a local package and immediately use it in another member without re-publishing.

## Lock Files

When you run `fglpkg install`, a `fglpkg.lock` file is created alongside your `fglpkg.json`. The lock file pins:

- Exact resolved versions of every BDL package
- Download URLs and SHA256 checksums
- The Genero version used at resolution time

This ensures reproducible installs across machines and CI environments. Commit `fglpkg.lock` to version control.

To bypass the lock and re-resolve everything:

```bash
fglpkg update
```

## Package Ownership

Packages can have multiple owners who are allowed to publish new versions.

### List Owners

```bash
fglpkg owner list myutils
```

### Add an Owner

```bash
fglpkg owner add myutils jdeveloper
```

### Remove an Owner

```bash
fglpkg owner remove myutils jdeveloper
```

## Troubleshooting

### "not logged in" when publishing

Make sure you have authenticated:

```bash
fglpkg login
```

Or set the `FGLPKG_PUBLISH_TOKEN` environment variable.

### Packages not found by Genero after install

Make sure your shell profile includes the `eval` line:

```bash
eval "$(fglpkg env)"
```

Restart your shell or run `source ~/.bashrc` after adding it.

### Stale lock file

If dependencies in `fglpkg.json` have changed and `fglpkg install` says the lock file is stale, it will automatically re-resolve. You can also force it:

```bash
fglpkg update
```

### Wrong Genero version detected

Override the detected version:

```bash
export FGLPKG_GENERO_VERSION=4.1.0
fglpkg install
```

### Using a private registry

Point fglpkg at your registry:

```bash
export FGLPKG_REGISTRY=https://registry.example.com
```

Add this to your shell profile for persistence.

### Checking the installed version

```bash
fglpkg version
```

This shows the version and build number embedded at compile time.

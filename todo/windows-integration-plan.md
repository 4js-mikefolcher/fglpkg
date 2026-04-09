# Windows Integration Plan

Scoped changes needed for Windows support without requiring bash or another Unix-like shell environment.

## High Priority

### `fglpkg run` — shebang/interpreter resolution

**File:** `internal/cli/cli.go` (cmdRun, ~line 1144)

`exec.Command(scriptPath, scriptArgs...)` relies on Unix shebangs (`#!/bin/bash`) to pick an interpreter. On Windows, a file like `migrate.sh` with a shebang won't execute.

**Fix:** Detect the file extension on Windows and route through the appropriate interpreter:
- `.sh` -> `bash` (Git Bash or WSL)
- `.py` -> `python`/`python3`
- `.bat`/`.cmd` -> native execution
- `.ps1` -> `powershell -File`
- No extension or unknown -> fail with a clear error listing supported extensions

## Medium Priority

### Home directory — hardcoded `/`

**File:** `internal/cli/cli.go` (fglpkgHome, ~line 1518)

`home + "/.fglpkg"` uses string concatenation instead of `filepath.Join`. Produces `C:\Users\mike/.fglpkg` on Windows — technically works but is inconsistent and could break path-strict tools.

**Fix:** Replace with `filepath.Join(home, ".fglpkg")`.

### Genero version detection — missing `.exe`

**File:** `internal/genero/genero.go` (~lines 48, 59)

Looks for `fglcomp` by explicit path (`filepath.Join(fgldir, "bin", "fglcomp")`). On Windows the binary is `fglcomp.exe`. Go's `exec.LookPath` adds `.exe` automatically for PATH lookups, but explicit paths need the extension.

**Fix:** Append `.exe` on Windows when constructing the explicit path:
```go
name := "fglcomp"
if runtime.GOOS == "windows" {
    name += ".exe"
}
```

## Low Priority

### Help text assumes bash

**File:** `internal/cli/cli.go` (printUsage, ~line 1505)

`printUsage()` says `Add to ~/.bashrc: eval "$(fglpkg env --global)"`. On Windows this is meaningless.

**Fix:** Add OS-aware setup instructions. For PowerShell: `fglpkg env --global | Invoke-Expression`, or for Command Prompt: `fglpkg env --global` and set the displayed variables manually.

### Credential file permissions

**File:** `internal/credentials/credentials.go` (~lines 67, 75)

Writes with mode `0600` which is correct on Unix but ignored on Windows. Credentials file may be world-readable by default on NTFS.

**Fix:** Optionally set Windows ACLs to restrict access to the current user. This is complex (requires `golang.org/x/sys/windows`) and may not be worth the effort unless security hardening is a priority.

### Unix permission modes (cosmetic)

**Files:** `lockfile.go`, `manifest.go`, `workspace.go`, `installer.go`

`os.MkdirAll(..., 0755)` and `os.WriteFile(..., 0644)` are silently ignored on Windows. Not broken, but inconsistent.

**Fix:** No functional change needed. Document that these modes are Unix-only.

## Already Handled

- `fglpkg env` output — `env.go` already branches on `runtime.GOOS == "windows"` to emit `SET` instead of `export`
- `chmod +x` after install — `installer.go` already skips on Windows
- Installer tests — already `t.Skip` on Windows
- GST paths — Genero Studio uses `$(variable)/path` with `;` separators regardless of OS and translates to the target OS

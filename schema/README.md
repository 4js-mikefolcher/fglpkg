# fglpkg JSON Schemas

`fglpkg.schema.json` describes the shape of `fglpkg.json`. Point your editor at it to get autocomplete, hover docs, and inline validation.

## VS Code

Add the reference to your manifest (`$schema` works in any JSON file):

```json
{
  "$schema": "https://fglpkg.io/schema/v1/fglpkg.schema.json",
  "name": "myproject",
  "version": "0.1.0",
  ...
}
```

The URL above is reserved for the canonical hosted copy. Until it is published, point at the local file instead:

```json
{
  "$schema": "./schema/fglpkg.schema.json",
  "name": "myproject",
  "version": "0.1.0"
}
```

Or add a repo-wide mapping in `.vscode/settings.json` so every `fglpkg.json` gets the schema automatically:

```json
{
  "json.schemas": [
    {
      "fileMatch": ["fglpkg.json"],
      "url": "./schema/fglpkg.schema.json"
    }
  ]
}
```

## JetBrains IDEs (IntelliJ, GoLand, WebStorm, …)

**Settings → Languages & Frameworks → Schemas and DTDs → JSON Schema Mappings → `+`**

- Name: `fglpkg`
- Schema file: `schema/fglpkg.schema.json`
- Schema version: Draft 7
- File path pattern: `fglpkg.json`

## Neovim / LSP

Configure the `jsonls` language server with a `settings.json.schemas` entry pointing at the file or URL, same shape as the VS Code example.

## Validation from the CLI

The fglpkg CLI validates the manifest on every `fglpkg install` / `fglpkg publish` via its strict parser — the schema is purely an editor aid. If the CLI accepts a manifest but the schema rejects it (or vice versa), that is a bug: please report it.

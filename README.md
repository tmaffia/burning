# Burning

A small CLI for checking coding-agent subscription usage from the terminal or an agent.

```text
openai   5h [###.......]  28% usage · 3h12m │ 7d [######....]  61% usage · 4d6h
ollama   5h [#.........]   5% usage │ 7d [#.........]   5% usage
```

Reports OpenAI Codex, Ollama Cloud, and Claude usage across session and weekly windows.

## Install

### Go

```sh
go install github.com/tmaffia/burning@latest   # install or update
rm "$(go env GOPATH)/bin/burning"              # uninstall
```

### Release archive

Download an archive and its `SHA256SUMS` file from the [latest release](https://github.com/tmaffia/burning/releases/latest). Replace `VERSION`, `OS` (`darwin` or `linux`), and `ARCH` (`amd64` or `arm64`) for your machine:

```sh
VERSION=v0.1.0 OS=darwin ARCH=arm64
ARCHIVE="burning_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -LO "https://github.com/tmaffia/burning/releases/download/${VERSION}/${ARCHIVE}"
curl -LO "https://github.com/tmaffia/burning/releases/download/${VERSION}/SHA256SUMS"
grep " ${ARCHIVE}$" SHA256SUMS | shasum -a 256 -c -
tar -xzf "$ARCHIVE"
```

## Usage

```sh
burning            # human report
burning --json     # machine-readable report
burning --version  # "dev" unless stamped at build time
```

## Configuration

Providers are read from `$XDG_CONFIG_HOME/burning/config.json` (`~/Library/Application Support/burning/config.json` on macOS):

```json
{"providers": ["openai", "ollama", "claude"]}
```

A missing file means no providers are configured. `burning login` adds its selected Provider to this list.

## Credentials

```sh
burning login
burning logout
```

Run `burning login` before your first usage report. Both commands select a Provider interactively. `login` adds that Provider to
`config.json`. OpenAI Codex and Claude login each open a browser for OAuth and
store the resulting Credential only in `auth.json` beside `config.json`. Claude
reports the shared subscription allowance consumed across Claude.ai, Claude
Desktop, and Claude Code. Ollama Cloud login opens its [API-key page](https://ollama.com/settings/keys),
verifies the entered Credential, and stores it there too; the directory and
credential file are owner-only (`0700` and `0600`). `OLLAMA_API_KEY` overrides
that stored Ollama Cloud Credential for the current invocation.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | All providers reported usage (or none are configured) |
| 1 | One or more providers failed |
| 2 | The command did not run (bad flags, unreadable config, or a `login`/`logout` that could not proceed — no terminal, unknown Provider, or a `login` missing a credential) |

## JSON output

```sh
burning --json
```

`burning --json` emits schema `burning.usage.v1`: Usage and Remaining Allowance percentages at full normalized precision, `resets_at`/`remaining_seconds` only when the provider exposes a reset time, failing providers as structured `errors`, and never ANSI escapes.

```json
{
  "schema": "burning.usage.v1",
  "generated_at": "2026-08-12T06:00:00Z",
  "providers": [
    {
      "name": "openai",
      "windows": [
        {
          "name": "session",
          "duration_seconds": 18000,
          "usage_percent": 28.4,
          "remaining_allowance_percent": 71.6,
          "resets_at": "2026-08-12T09:12:00Z",
          "remaining_seconds": 11520
        }
      ]
    }
  ],
  "errors": [
    {"provider": "ollama", "message": "timeout after 10s"}
  ]
}
```

## Building

```sh
go build -ldflags "-X main.version=v0.1.0" -o burning .
```

> Burning relies on undocumented provider usage endpoints that may change.

The domain vocabulary is in [`CONTEXT.md`](./CONTEXT.md).

## Agent skill

Install the Burning skill for your agent with the [skills](https://github.com/vercel-labs/skills) CLI:

```sh
npx skills add tmaffia/burning  # install
npx skills update burning       # update
npx skills remove burning       # uninstall
```

The `burning` executable must also be on `PATH`. The skill runs `burning --json` for Usage requests and directs credential setup to `burning login` in your terminal.

## Development

```sh
make check
```

### End-to-end checks

`make e2e` runs the complete Go suite, then invokes `burning --json` once
against every configured Provider. It requires configured Providers, working
Credentials, and network access. It uses the existing configuration and
Credentials; OpenAI and Claude may refresh their Credentials automatically.
Login and logout are intentionally excluded.

Implementation is tracked in [GitHub Issues](https://github.com/tmaffia/burning/issues).

## License

[MIT](./LICENSE)

# Burning

A small CLI for checking coding-agent subscription usage from the terminal or an agent.

```text
OpenAI  5h ▕███░░░░░░░▏ 28% usage · 3h12m │ 7d ▕██████░░░░▏ 61% usage · 4d6h
Ollama  5h ▕█░░░░░░░░░▏  5% usage         │ 7d ▕█░░░░░░░░░▏  5% usage
```

## Planned v0.1

- OpenAI Codex and Ollama Cloud
- Standalone `burning login` and `burning logout`
- Usage bars for humans and versioned JSON for agents
- Session and weekly usage windows, with reset countdowns when available
- macOS and Linux binaries for AMD64 and ARM64, including WSL support
- A small [Agent Skills](https://agentskills.io/) integration

```text
burning            # human report
burning --json     # machine-readable report
burning --version  # "dev" unless stamped at build time
```

## Configuration

Providers are read from `$XDG_CONFIG_HOME/burning/config.json` (`~/Library/Application Support/burning/config.json` on macOS):

```json
{"providers": ["openai", "ollama"]}
```

A missing file means no providers are configured.

## Credentials

```sh
burning login
burning logout
```

Both commands select a Provider interactively. Ollama Cloud login opens its
[API-key page](https://ollama.com/settings/keys), verifies the entered key, and
stores it only in `auth.json` beside `config.json`; the directory and credential
file are owner-only (`0700` and `0600`). `OLLAMA_API_KEY` overrides that stored
Ollama Cloud Credential for the current invocation.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | All providers reported usage (or none are configured) |
| 1 | One or more providers failed |
| 2 | The command did not run (bad flags, unreadable config, or a `login`/`logout` that could not proceed — no terminal, unknown Provider, or a `login` missing a credential) |

## JSON output

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

The domain vocabulary is in [`CONTEXT.md`](./CONTEXT.md); supporting research is under [`docs/research/`](./docs/research/).

## Development

Implementation is tracked in [GitHub Issues](https://github.com/tmaffia/burning/issues).

## License

[MIT](./LICENSE)

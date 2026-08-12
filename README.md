# Burning

A small CLI for checking coding-agent subscription usage from the terminal or an agent.

```text
OpenAI  5h ▕███░░░░░░░▏ 28% used · 3h12m │ 7d ▕██████░░░░▏ 61% used · 4d6h
Ollama  5h ▕█░░░░░░░░░▏  5% used         │ 7d ▕█░░░░░░░░░▏  5% used
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

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | All providers reported usage (or none are configured) |
| 1 | One or more providers failed |
| 2 | Fatal error (bad flags, unreadable config) |

## JSON output

`burning --json` emits schema `burning.usage.v1`: normalized percents at full precision, `resets_at`/`remaining_seconds` only when the provider exposes a reset time, failing providers as structured `errors`, and never ANSI escapes.

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
          "used_percent": 28.4,
          "remaining_percent": 71.6,
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

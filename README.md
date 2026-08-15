# Burning

A small CLI for checking coding-agent subscription usage from the terminal or an agent.

```text
ollama      45m ▕░░░░░░░░░░▏   4%        ·     7d ▕██░░░░░░░░▏  19%
openai                                   ·     7d ▕██████░░░░▏  61%  6d12h
claude       5h ▕██░░░░░░░░▏  20%  7h10m ·     7d ▕█░░░░░░░░░▏   8%  5d10h
```

Reports OpenAI Codex, Ollama Cloud, Claude, and SuperGrok usage across session and weekly windows.

> Burning relies on undocumented provider usage endpoints that may change.

## Install

```sh
go install github.com/tmaffia/burning@latest
```

Binary releases are available on [GitHub](https://github.com/tmaffia/burning/releases).

## Usage

```sh
burning            # human report
burning --json     # machine report
burning login      # setup credentials
burning logout
```

Run `burning login` first. Providers are configured in `$XDG_CONFIG_HOME/burning/config.json`.

`burning --help` for flags and exit codes.

## JSON

`burning --json` emits `burning.usage.v1`:

<details>
<summary>Example output</summary>

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
</details>

## Agent skill

Requires `burning` on `PATH`. Install via [skills](https://github.com/vercel-labs/skills):

```sh
npx skills add tmaffia/burning
```

Domain vocabulary: [`CONTEXT.md`](./CONTEXT.md).

## License

[MIT](./LICENSE)

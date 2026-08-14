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

### Go

```sh
go install github.com/tmaffia/burning@latest
```

```sh
rm "$(go env GOPATH)/bin/burning"
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
burning --version
```

```sh
burning login
burning logout
```

Run `burning login` before the first report. Both commands pick a Provider interactively. `login` adds it to `config.json` and stores the Credential in `auth.json` beside it (owner-only `0700`/`0600`). OpenAI Codex, Claude, and SuperGrok use browser OAuth; Ollama Cloud opens its [API-key page](https://ollama.com/settings/keys) and verifies the key. `OLLAMA_API_KEY` overrides the stored Ollama Credential for one run.

Providers live in `$XDG_CONFIG_HOME/burning/config.json` (`~/Library/Application Support/burning/config.json` on macOS):

```json
{"providers": ["openai", "ollama", "claude", "grok"]}
```

A missing file means no providers are configured.

Claude reports the shared subscription allowance across Claude.ai, Claude Desktop, and Claude Code. SuperGrok reports the shared weekly SuperGrok pool.

```sh
burning --help
```

covers flags and exit codes.

## JSON

`burning --json` emits schema `burning.usage.v1`:

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

Percentages stay at full normalized precision. `resets_at` / `remaining_seconds` appear only when the provider exposes a reset time. Failures land in `errors`. No ANSI.

## Agent skill

Requires the `burning` binary on `PATH`. Install with the [skills](https://github.com/vercel-labs/skills) CLI:

```sh
npx skills add tmaffia/burning
```

```sh
npx skills update burning
```

```sh
npx skills remove burning
```

The skill runs `burning --json` and points credential setup at `burning login`.

Domain vocabulary: [`CONTEXT.md`](./CONTEXT.md).

## License

[MIT](./LICENSE)

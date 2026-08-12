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
burning
burning --json
burning login
burning logout
```

> Burning relies on undocumented provider usage endpoints that may change.

The domain vocabulary is in [`CONTEXT.md`](./CONTEXT.md); supporting research is under [`docs/research/`](./docs/research/).

## Development

Implementation is tracked in [GitHub Issues](https://github.com/tmaffia/burning/issues).

## License

[MIT](./LICENSE)

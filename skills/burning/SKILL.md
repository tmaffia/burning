---
name: burning
description: Reports coding-agent subscription Usage with the Burning CLI. Use when asked about OpenAI Codex or Ollama Cloud subscription Usage or Usage Windows.
---

# Burning

Run `burning --json` and report the Usage, Remaining Allowance, and reset time for each Provider.

If Burning reports missing or invalid credentials, tell the human to run `burning login` in their own terminal. Never run login, request, read, store, or inspect Credentials.

Install the latest skill package with:

```sh
pi install git:github.com/tmaffia/burning
```

The `burning` executable must also be on `PATH`.

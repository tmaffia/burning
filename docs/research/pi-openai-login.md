# Pi's OpenAI Codex login

Pi authenticates ChatGPT Plus/Pro users directly; it does not invoke or require the Codex CLI ([provider documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/providers.md)).

Its OpenAI Codex provider offers two OAuth methods ([implementation](https://github.com/earendil-works/pi/blob/main/packages/ai/src/auth/oauth/openai-codex.ts)):

- **Browser login:** Authorization Code with PKCE, a random `state`, and a temporary callback server at `http://localhost:1455/auth/callback`. Pi opens the authorization URL and also accepts a pasted authorization code or redirect URL for remote environments.
- **Device-code login:** Pi requests a user code, opens `https://auth.openai.com/codex/device`, polls until authorization completes, and exchanges the resulting authorization code.

Both methods exchange at `https://auth.openai.com/oauth/token` and receive access, refresh, and expiry values. Pi extracts the ChatGPT account ID from the access-token JWT, stores the credential in `~/.pi/agent/auth.json` with mode `0600`, and refreshes expired access tokens under a cross-process file lock ([credential storage](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/auth-storage.ts)).

The OAuth client ID and core flow are also present in OpenAI's official Codex source ([client and refresh](https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/manager.rs), [browser flow](https://github.com/openai/codex/blob/main/codex-rs/login/src/server.rs), [device flow](https://github.com/openai/codex/blob/main/codex-rs/login/src/device_code_auth.rs)); they are not Pi-specific.

A live, redacted check confirmed that a Pi-issued access token and account ID can call `GET https://chatgpt.com/backend-api/wham/usage` directly and receive usage windows with used percentages, durations, and reset timestamps. No Codex installation is needed.

## WSL

WSL 2 forwards Linux guest ports to Windows `localhost`, and WSL can launch Windows executables directly ([networking](https://learn.microsoft.com/en-us/windows/wsl/networking); [interop](https://learn.microsoft.com/en-us/windows/wsl/filesystems#run-windows-tools-from-linux)). Burning can therefore run its callback server inside WSL and open the authorization URL with a Windows browser through WSL interop.

## Recommendation

Start with local browser PKCE, including WSL browser launching, and add device-code login only if headless users need it. Store credentials only in Burning's own permission-restricted auth file, refresh them before querying usage, and never inspect another agent's credential store.

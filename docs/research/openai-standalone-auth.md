# Standalone ChatGPT subscription OAuth

## Finding

Burning cannot treat the browser OAuth flow used by Codex as a supported,
general-purpose OpenAI integration.

OpenAI documents ChatGPT subscription sign-in only for its first-party Codex
surfaces: the ChatGPT desktop app, Codex CLI, and IDE extension. Its documented
programmatic alternative is a Platform API key, which uses standard API pricing
rather than ChatGPT subscription allowance ([Authentication](https://developers.openai.com/codex/auth)).

The open-source Codex CLI contains a PKCE browser flow and its OAuth client ID,
but that is implementation detail for a first-party client, not a published
third-party OAuth contract. Pi independently reimplements that request shape
with `originator=pi` ([source](https://github.com/earendil-works/pi/blob/main/packages/ai/src/auth/oauth/openai-codex.ts)).
This demonstrates a technical route, not a stable integration contract: the
accompanying OpenAI guide explicitly says that its managed-auth procedure does
**not** apply to “generic OAuth clients outside Codex” and instructs trusted
automation to create credentials with `codex login` and let Codex refresh them,
rather than calling the OAuth token endpoint itself
([Maintain Codex account auth in CI/CD](https://developers.openai.com/codex/auth/ci-cd-auth)).

There is no OpenAI document describing developer registration of an OAuth client
that grants a user's personal ChatGPT/Codex subscription allowance to a
standalone third-party CLI. Registering a ChatGPT App is the reverse
integration—ChatGPT authenticates to the app's external OAuth provider—not an
OAuth client-registration mechanism for a third-party app to obtain ChatGPT
subscription credentials.

## Supported paths

1. **ChatGPT subscription allowance:** use an official Codex local surface to
   perform and maintain the login. This contradicts issue #4's “no Codex
   installation” requirement.
2. **Programmatic standalone client:** use a Platform API key. This is supported
   but reports/payments follow the API organization, not ChatGPT subscription
   Usage ([Authentication](https://developers.openai.com/codex/auth)).
3. **Business or Enterprise trusted automation:** a workspace admin can enable
   Codex access tokens. OpenAI documents them for Codex CLI or app-server local
   workflows, not as a general ChatGPT subscription OAuth replacement
   ([Codex access tokens](https://developers.openai.com/codex/enterprise/access-tokens)).

## Recommendation

The supported design remains an official Codex local surface or a Platform API
key. If Burning intentionally adopts Pi-compatible browser OAuth anyway, treat
it as experimental: preserve exact request parity, keep a live manual check,
and expect upstream changes or account policy to break it. Do not represent it
as a stable third-party OAuth integration.

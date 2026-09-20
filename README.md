# ai-lb

Load balancer and OpenAI-compatible proxy for pooled AI coding accounts.

> **Status: early development, not yet runnable.** There is no working load balancer here yet — this repository currently holds only the project scaffold (docs, license, contribution basics). Everything below describes intent, not implemented behavior.

**License:** source-available under [PolyForm Noncommercial 1.0.0](LICENSE) — see [NOTICE](NOTICE). Noncommercial use, modification, and distribution are permitted with attribution; commercial use requires separate written permission.

## What is ai-lb?

`ai-lb` is intended to become a local, self-hosted load balancer and proxy for accounts from AI coding tools/providers.

The intended flow:

```text
Accounts -> ai-lb -> OpenAI-compatible API -> any compatible client
```

Concretely, the goal is:

1. You add several of your own accounts to ai-lb. Initially targeted providers: **Antigravity** and **Codex**.
2. ai-lb stores and manages that account pool locally.
3. You enable an OpenAI-compatible API in ai-lb.
4. ai-lb exposes a base URL, an API key, and OpenAI-compatible endpoints.
5. You plug that base URL + key into any compatible client: a coding agent, IDE/plugin, the OpenAI SDK, a CLI, or any other app speaking the OpenAI-compatible API.
6. ai-lb picks a suitable account per request and handles routing / load balancing.

Nothing in steps 1–6 is implemented yet.

## How it is intended to work

The planned request path is:

1. A client sends an OpenAI-compatible request to ai-lb with the ai-lb API key.
2. ai-lb authenticates the request against its own keys.
3. ai-lb selects an account from the pool according to availability, quota/rate-limit signals, recent errors, temporary bans, and the configured balancing strategy.
4. ai-lb forwards the request through the matching provider adapter.
5. On retryable failures, ai-lb retries or fails over to another account.
6. ai-lb returns the provider response in OpenAI-compatible form.

Planned architectural direction (not yet built):

- provider-neutral core (pooling, routing, auth, API surface);
- one adapter per provider (Codex, Antigravity, later others);
- local-first credential and state storage.

## Planned features

All of the following are **planned, not implemented**:

- Multiple accounts per provider
- Codex account pool
- Antigravity account pool
- Provider adapters (Codex, Antigravity, future providers)
- Account health / status tracking
- Quota awareness (quota / rate-limit signals)
- Configurable load-balancing strategies
- Retries
- Failover across accounts
- API-key authentication for the local API
- OpenAI-compatible API surface
- Self-hosted / local-first operation

The scope will be narrowed once implementation starts. Nothing here is a promise of a specific release.

## OpenAI-compatible API

The intent is for ai-lb to expose OpenAI-compatible endpoints (such as chat/completions-style routes and model listing) so existing clients work without custom integrations.

No endpoints exist yet, and no base URL, port, key format, or model names are defined. Client setup guides will be added once the first runnable version lands.

## Architecture

No code, no final tech stack, no directory layout to document yet.

The only decision recorded so far: keep the core provider-neutral and isolate provider specifics in adapters. Details (language, storage, API framework, config format) are unresolved and will be documented when chosen.

## Security

ai-lb will handle sensitive material: OAuth access/refresh tokens, API keys, session data, account exports, and provider credentials.

Rules that already apply to this repository, even before any code exists:

- Never commit real credentials, tokens, keys, session files, or account exports.
- Never paste them into GitHub Issues, PRs, logs, or screenshots.
- Use `.env.example`-style placeholders if example config is ever added.

See [SECURITY.md](SECURITY.md) for the full policy. A private vulnerability-reporting channel will be documented before the first release.

## Project status

- Scaffold only: `README.md`, `LICENSE` (PolyForm Noncommercial 1.0.0), `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `.gitignore`, `.editorconfig`, PR template.
- No implementation, no binaries, no packages, no releases, no changelog yet.
- Tech stack and milestones are undecided.

### Installation

ai-lb is currently in early development. Installation instructions will be added once the first runnable version is available.

## Development

There is nothing to build, test, or run yet.

Once implementation starts, this section will document prerequisites, checks, and the canonical workflow. Until then, see [CONTRIBUTING.md](CONTRIBUTING.md) for the basic fork/branch expectations.

## Contributing

Small, focused contributions to docs and scaffold are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

Do not include secrets or account data in issues or PRs. Larger architectural changes are best discussed in an issue first.

## References / Acknowledgements

- [Soju06/codex-lb](https://github.com/Soju06/codex-lb) — reference for account pooling, load balancing, and OpenAI-compatible API-key proxy design.
- [jlcodes99/cockpit-tools](https://github.com/jlcodes99/cockpit-tools) — reference for multi-account UX, Antigravity/Codex account lifecycle, and local-first quota/status handling.

These projects are inspiration and architectural/UX references only. No code, README text, or structure was copied from them; consult their own repositories for their licenses.

## License

Source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE).

You may use, modify, and distribute this project for noncommercial purposes with attribution. Commercial use requires separate written permission from the copyright holder — see [NOTICE](NOTICE):

```text
Required Notice: Copyright 2026 Dustin Corder
```

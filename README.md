# ai-lb

Load balancer and OpenAI-compatible proxy for pooled AI coding accounts.

> **Status: foundation only, not a working load balancer yet.** The Go web
> service foundation runs (control server + gateway listener + embedded web
> UI + SQLite settings). Everything provider-related below is still intent,
> not implemented behavior.

**License:** source-available under [PolyForm Noncommercial 1.0.0](LICENSE) — see [NOTICE](NOTICE). Noncommercial use and modification are permitted under the PolyForm Noncommercial License 1.0.0. Redistributed copies must retain the applicable license terms or license URL and all Required Notice lines. Commercial use requires separate written permission from the copyright holder.

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

## Current state

Implemented:

- Go service foundation (single `ai-lb` executable, no Node.js runtime needed)
- Embedded web UI (React + TypeScript + Vite build served by the control server)
- Control server (`http://127.0.0.1:8317` by default): web UI + management API
- Gateway listener (`http://127.0.0.1:8318` by default, `GET /health` only)
- SQLite settings (auto-created, migrated, validated; ports editable in the UI)

Not implemented:

- Codex integration
- Antigravity integration
- Routing / load balancing
- Quota tracking
- CLI switching
- OpenAI-compatible endpoints
- API access manager (clients, keys, policies)

## Running it

```bash
go build -o ai-lb ./cmd/ai-lb
./ai-lb
```

- Open `http://127.0.0.1:8317` for the web UI.
- `curl http://127.0.0.1:8318/health` should return `{"status":"ok"}`.
- Change ports in Settings; restart the service for new ports to take effect.
- The backend keeps running with no browser window open.

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

No OpenAI-compatible inference endpoints are implemented yet.
The current gateway exposes only `GET /health` on the configured gateway
listener (`http://127.0.0.1:8318` by default). Client setup guides will be
added once the first runnable version lands.

## Architecture

Runtime foundation (implemented): a Go service with two loopback-only
listeners — a control server (browser web UI + management API) and a
gateway listener (data plane). The web UI build is embedded in the binary.
SQLite holds settings and app metadata in the OS-appropriate app data
directory. See [ARCHITECTURE.md](ARCHITECTURE.md) and
[docs/RFC-0001-v0.1.md](docs/RFC-0001-v0.1.md) for the full direction,
including the future common router over Codex/Antigravity pools.

Design decisions that still stand: keep the core provider-neutral and
isolate provider specifics in adapters; local-first credential and state
storage.

## Security

ai-lb will handle sensitive material: OAuth access/refresh tokens, API keys, session data, account exports, and provider credentials.

Rules that already apply to this repository, even before any code exists:

- Never commit real credentials, tokens, keys, session files, or account exports.
- Never paste them into GitHub Issues, PRs, logs, or screenshots.
- Use `.env.example`-style placeholders if example config is ever added.

See [SECURITY.md](SECURITY.md) for the full policy, including private vulnerability reporting.

## Project status

- Foundation: Go service (`cmd/ai-lb`, `internal/…`), React web UI (`web/`), SQLite settings.
- Docs: `ARCHITECTURE.md`, `docs/RFC-0001-v0.1.md` (provider/routing design is future direction).
- Legal/scaffold: `LICENSE` (PolyForm Noncommercial 1.0.0), `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `.gitignore`, `.editorconfig`, PR template.
- No provider integrations, no releases, no changelog yet.

### Installation

ai-lb is currently in early development. Installation instructions will be added once the first runnable version is available.

## Development

Prerequisites: Go toolchain, Node.js + npm.

```bash
go test ./...
go vet ./...
```

```bash
npm --prefix web install
npm --prefix web run lint
npm --prefix web run test
npm --prefix web run build   # required before the production Go build
go build -o ai-lb ./cmd/ai-lb
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution policy.

## Contributing

Documentation fixes, bug reports, and feature discussions are welcome. External implementation/code contributions are temporarily not accepted until the contributor licensing / relicensing policy is established. See [CONTRIBUTING.md](CONTRIBUTING.md).

Do not include secrets or account data in issues or PRs. Larger architectural changes are best discussed in an issue first.

## References / Acknowledgements

- [Soju06/codex-lb](https://github.com/Soju06/codex-lb) — reference for account pooling, load balancing, and OpenAI-compatible API-key proxy design.
- [jlcodes99/cockpit-tools](https://github.com/jlcodes99/cockpit-tools) — reference for multi-account UX, Antigravity/Codex account lifecycle, and local-first quota/status handling.

These projects are inspiration and architectural/UX references only. No code, README text, or structure was copied from them; consult their own repositories for their licenses.

## License

Source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE).

Noncommercial use and modification are permitted under the PolyForm Noncommercial License 1.0.0. Redistributed copies must retain the applicable license terms or license URL and all Required Notice lines. Commercial use requires separate written permission from the copyright holder — see [NOTICE](NOTICE):

```text
Required Notice: Copyright 2026 Dustin Corder
```

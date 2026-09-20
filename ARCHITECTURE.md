# ai-lb Architecture (v0.1)

> `ARCHITECTURE.md` is the high-level overview; details and rationale live
> in [docs/RFC-0001-v0.1.md](docs/RFC-0001-v0.1.md). Provider, routing, and
> access-management sections below are future direction — the only running
> code is the Go service foundation (§1).

ai-lb is a **local, self-hosted Go service with a browser-based web UI**
that will manage pooled AI coding accounts (initially **Codex** and
**Antigravity**) and serve them through an **OpenAI-compatible local API**
protected by its own API-key access management.

```text
Accounts -> ai-lb -> OpenAI-compatible API -> any compatible client
```

Decision history: an earlier Tauri/Rust desktop direction was replaced
before implementation by a Go local service + browser UI. Git history
holds the old text.

## 1. Runtime foundation (current)

```text
Browser UI
    ↓  (same-origin management API, no CORS)
Go Control Server  →  ai-lb Core (settings, SQLite, future adapters)

OpenAI clients
    ↓  (future OpenAI-compatible routes; today GET /health only)
Go Gateway Server  →  ai-lb Core
```

- One `ai-lb` executable, no Node.js runtime on the user machine (the
  React + TypeScript + Vite build is embedded via `go:embed`).
- Control plane (web UI + management API) and gateway/data plane are
  separate loopback-only listeners (`127.0.0.1:8317` / `127.0.0.1:8318`
  by default; configurable, persisted in SQLite, applied on restart).
  No `0.0.0.0`, LAN, remote, or tunnel modes exist.
- SQLite holds `settings` + `app_metadata` (WAL mode, foreign keys,
  versioned migrations, startup validation). Secrets never go in SQLite.
- The backend lives fully without any open browser window; closing the
  browser changes nothing. Graceful shutdown on SIGINT/SIGTERM.
- Control-plane browser baseline: loopback `Host` required
  (DNS-rebinding guard), same-origin `Origin` required on state-changing
  methods, no CORS, small JSON body cap on settings writes. No Admin UI
  login yet.

## 2. Product scope (v0.1)

Four logical parts, one local service:

```text
ai-lb
├── Account Manager      # (future) pooled Codex / Antigravity accounts, plan, quota, health
├── CLI Manager          # (future) activate an account into the official client
├── Local API Gateway    # loopback listener; today GET /health only
└── API Access Manager   # (future) clients, API keys, access policies, usage accounting
```

Implemented: Go service shell + SQLite settings + web UI skeleton
(Dashboard, Accounts, Gateway, Settings) + gateway liveness. Everything
else in this document is direction, not code.

## 3. Core principles

- **Local-first.** Accounts, secrets, state, and usage live on the user's
  machine. No cloud control plane, no hosted SaaS, no telemetry of prompt
  content in v0.1 — or ever by default.
- **Provider-neutral core, provider-specific adapters.** Pooling, routing,
  auth, policies, and accounting are generic. All upstream specifics
  (credential layout, quota shapes, client processes) hide behind a
  `ProviderAdapter` contract.
- **CLI switching and Local API routing are independent.** The active CLI
  account and the Local API pool do not have to match. The gateway never
  rewrites active CLI credentials per request; it holds independent access
  to pooled credentials.
- **Secrets separated from metadata.** The account record carries only a
  `credentials_ref`. Token material lives in OS credential storage behind a
  `SecretStore` abstraction, never as plaintext in SQLite.
- **Fail safe.** CLI switching is a transaction with backup, verify, and
  rollback. Routing degrades via cooldown and failover, never by guessing.
- **Honest models.** Subscription and quota models only expose what providers
  actually return. No invented billing, renewal, or expiry fields.
- **Minimal v0.1.** One Go service process, one SQLite file, loopback-only
  listeners. No daemon/service wrapper, no LAN/remote exposure, no billing
  platform.

## 4. System diagram (target; only settings/UI/liveness exist today)

```text
┌──────────────────────── ai-lb (single Go service) ─────────────────────────┐
│                                                                            │
│  Browser UI ──► Control Server ──┬──► ProviderAdapters ──► Codex upstream  │
│  (same-origin)          │        │                  └─► Antigravity upstream│
│                         │        │                                          │
│                         │        ├──► SecretStore ──► OS keychain / cred mgr │
│                         │        │                                          │
│                         │        ├──► SQLite ──► accounts, pools, keys,      │
│                         │        │              policies, usage (metadata)   │
│                         │        │                                          │
│                         │        ├──► ProcessManager ──► official CLI clients│
│                         │        │                                          │
│                         │        └──► QuotaRefresher (background task)       │
│                         │                                                  │
│                         └────── Gateway Server (loopback, :port) ◄──────────┼── clients
│                                    │ today: GET /health only; later:        │
│                                    │ auth → policy → routing → upstream     │
└────────────────────────────────────────────────────────────────────────────┘
```

Future design allows one ai-lb API key to use both provider pools:

```text
Codex accounts ─┐
                ├─→ common router → gateway response
Antigravity ────┘
```

allowed only where request/model capabilities match the account pool.

## 5. Major components

| Component | State |
|---|---|
| Control server + web UI | Implemented (serves UI, settings API, gateway liveness proxy) |
| Gateway listener | Implemented as liveness only (`GET /health`) |
| SQLite settings store | Implemented (migrations, WAL, validation) |
| Account Manager | Future: CRUD for pooled accounts; plan/subscription, quota windows, health; pool participation flags |
| CLI Manager | Future: safe `activateAccount` transaction (validate → backup → stop/reload → activate → restart → verify → rollback) |
| Provider adapters | Future: `CodexAdapter`, `AntigravityAdapter` behind the shared contract |
| API Access Manager | Future: `ApiClient` / `ApiKey` / `AccessPolicy` / `UsageRecord`; rotation; prefix+verifier storage, show-once secrets |
| Quota refresher | Future: periodic per-account polling with backoff; stale flags |
| Process manager | Future: detect/stop/start/verify official clients; own backup/restore |

## 6. Provider adapters (future)

The core defines a `ProviderAdapter` contract (accounts, subscription,
quota, activation, client process control, validation) plus a
`ProviderCapabilities` flag set (`cliSwitching`, `quota`,
`subscriptionInfo`, `localApi`, `tokenRefresh`, `safeRetry`, …).
Capabilities not supported by a provider are explicit, not silent. Exact
credential locations and quota mappings are discovered at implementation
time and stay inside the adapter.

## 7. Data / security model

- **SQLite** holds metadata and state (today: settings; later: accounts
  with `credentials_ref`, pools, gateway config, API clients/keys as
  prefix + verifier only, policies, usage records without
  prompt/response bodies).
- **OS credential storage** (macOS Keychain, Windows Credential Manager,
  Linux Secret Service/keyring) holds OAuth/API token material behind
  `SecretStore { put, get, delete }`. No supported store means fail
  closed. Request-time resolution goes through short-lived in-memory
  credential sessions — never an OS lookup per HTTP request, never
  secrets in SQLite.
- Local API keys are generated server-side, shown once, stored as
  SHA-256 verifier (constant-time compare, no password KDF).
- Request authorization runs fully **before** upstream routing:
  authenticate → enabled/expiry → IP policy → rate → concurrency →
  token/budget → model/pool policy → routing → upstream.

## 8. Local API (target; only liveness today)

- Loopback-only listeners; gateway default `http://127.0.0.1:8318`.
- Compatibility target (not implemented): model listing
  (`/v1/models`), Responses API (`/v1/responses`), and Chat Completions
  (`/v1/chat/completions`) surfaces required by common coding clients.
  Further routes are added explicitly; unknown `/v1/*` routes get a
  controlled unsupported-route response, never a blind pass-through.
- Deterministic routing when built: enabled → healthy → quota available
  → cooldown expired → session affinity (stable session identifier
  only) → priority/available quota → select. Safe retry and failover
  only (never after ambiguous sends or into an exposed stream);
  per-account cooldown; streaming passed through; every request
  accounted as a provider-neutral `RequestUsage` record.

### Future routing requirements (documented, not built)

- Automatic account failover on exhausted quota.
- Optional cross-provider routing.
- Session/conversation affinity (stable identifiers only; no prompt
  history stored for affinity).
- Capability-based routing (model/request capabilities vs pool).
- Image input where the upstream model supports it.
- System/developer instructions where supported.
- Future video capability tracked without promising v0.1 video support.

## 9. API access management (future)

- One `ApiClient` owns multiple `ApiKey`s for rotation (current +
  retiring).
- `AccessPolicy` per key or client: expiry, request rate, concurrency,
  token budgets (input/output/total), monetary budgets (only when the
  provider supplies cost), model allow/deny, pool/account restriction,
  IP allow/deny (never the sole boundary).
- `UsageRecord` aggregates per-request `RequestUsage` rows.

### Future User Portal (concept, not built)

- **Admin UI** (this foundation's UI grows into it): accounts, routing,
  quotas, API clients, policies, settings.
- **User Portal** (future, separate surface): own usage, remaining
  limits, allowed models, API endpoint, key status.

## 10. Process model

One Go service process: control server, gateway listener, SQLite, and
(later) quota refresher and process manager. No desktop shell, no
webview, no sidecar, no daemon/service wrapper in v0.1. The user runs
`./ai-lb` in the foreground (or under their own supervision);
autostart/headless behavior is a maintainer decision tracked in the RFC.

## 11. Explicit non-goals for v0.1

Cloud control plane; hosted SaaS; Kubernetes / multi-node / distributed
routing; PostgreSQL server; Redis; billing platform and payment
processing; teams/organizations; own remote-relay infrastructure; mobile
apps; desktop shell / webview wrapper; LAN/remote exposure (a future
`ApiExposure { Localhost, LAN, RemoteTunnel }` switch may be reserved —
v0.1 is `Localhost` only); prompt/response body storage.

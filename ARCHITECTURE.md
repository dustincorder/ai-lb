# ai-lb Architecture (v0.1)

> Design document, not a status report. Nothing described here is implemented yet.
> `ARCHITECTURE.md` is the high-level overview; details and rationale live in
> [docs/RFC-0001-v0.1.md](docs/RFC-0001-v0.1.md).

ai-lb is planned as a **local, self-hosted desktop application** that manages
pooled AI coding accounts (initially **Codex** and **Antigravity**) and serves
them through an **OpenAI-compatible local API** protected by its own API-key
access management.

```text
Accounts -> ai-lb -> OpenAI-compatible API -> any compatible client
```

## 1. Product scope (v0.1)

Four logical parts, one desktop app:

```text
ai-lb
├── Account Manager      # pooled Codex / Antigravity accounts, plan, quota, health
├── CLI Manager          # activate an account into the official Codex / Antigravity client
├── Local API Gateway    # OpenAI-compatible HTTP API on loopback, routed over account pools
└── API Access Manager   # clients, API keys, access policies, usage accounting
```

v0.1 covers: desktop shell + persistence + provider abstraction; Codex and
Antigravity account management with quota and CLI switching; a loopback
OpenAI-compatible gateway with account pools; API-key access management with
policies and usage records.

## 2. Core principles

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
  `SecretStore` abstraction, never as plaintext in SQLite by default.
- **Fail safe.** CLI switching is a transaction with backup, verify, and
  rollback. Routing degrades via cooldown and failover, never by guessing.
- **Honest models.** Subscription and quota models only expose what providers
  actually return. No invented billing, renewal, or expiry fields.
- **Minimal v0.1.** One desktop process, one SQLite file, loopback-only
  gateway. No daemon/sidecar, no LAN/remote exposure, no billing platform.

## 3. System diagram

```text
┌────────────────────────────── ai-lb (single desktop process) ──────────────┐
│                                                                            │
│  GUI (webview) ──► Core ──┬──► ProviderAdapters ──► Codex upstream         │
│                   │       │                      └─► Antigravity upstream  │
│                   │       │                                                │
│                   │       ├──► SecretStore ──► OS keychain / credential mgr │
│                   │       │                                                │
│                   │       ├──► SQLite ──► accounts, pools, keys, policies,  │
│                   │       │              usage (metadata only)              │
│                   │       │                                                │
│                   │       ├──► ProcessManager ──► official CLI clients      │
│                   │       │       (stop / start / restart / verify)         │
│                   │       │                                                │
│                   │       └──► QuotaRefresher (background task)             │
│                   │                                                        │
│                   └────── Local API Gateway (loopback HTTP, :port/v1) ◄─────┼── clients
│                              │ auth → policy → routing → adapter → upstream │
└────────────────────────────────────────────────────────────────────────────┘
```

## 4. Major components

| Component | Role |
|---|---|
| Account Manager | CRUD for pooled accounts; plan/subscription, quota windows, health; manual refresh; enable/disable pool participation |
| CLI Manager | Safe `activateAccount` transaction per provider: validate → backup → stop/reload → activate → restart → verify → rollback on failure |
| Provider adapters | `CodexAdapter`, `AntigravityAdapter` implementing the shared `ProviderAdapter` contract with per-provider capabilities |
| Local API Gateway | Loopback OpenAI-compatible HTTP API; key auth, policy checks, deterministic routing, retry/failover, streaming, usage accounting |
| API Access Manager | `ApiClient` / `ApiKey` / `AccessPolicy` / `UsageRecord`; rotation via multiple keys per client; prefix+verifier storage, show-once secrets |
| Quota refresher | Periodic, per-account quota/subscription polling with backoff; marks snapshots stale on failure |
| Process manager | Detects, stops, starts, and verifies official client processes; owns client-state backup/restore |

## 5. Provider adapters

The core defines a `ProviderAdapter` contract (accounts, subscription, quota,
activation, client process control, validation) plus a `ProviderCapabilities`
flag set (`cliSwitching`, `quota`, `subscriptionInfo`, `localApi`,
`tokenRefresh`, …). Capabilities not supported by a provider are explicit, not
silent. v0.1 ships `CodexAdapter` and `AntigravityAdapter` (design only — exact
credential locations and quota mappings are discovered at implementation time).

## 6. Data / security model

- **SQLite** holds metadata and state: accounts (with `credentials_ref`, never
  secrets), pools, gateway config, API clients/keys (prefix + hash only),
  policies, usage records (no prompt/response bodies by default).
- **OS credential storage** (macOS Keychain, Windows Credential Manager, Linux
  Secret Service/keyring) holds OAuth/API token material behind
  `SecretStore { put, get, delete }`. No supported store means fail closed:
  secret persistence is unavailable and the GUI explains why. No custom
  encrypted secret file in v0.1.
- The gateway resolves pooled credentials through short-lived in-memory
  credential sessions over `SecretStore` — never an OS keychain lookup per
  HTTP request, never secrets in SQLite.
- Local API keys are generated server-side, shown once, stored as SHA-256
  verifier (constant-time compare, no password KDF).
- Request authorization runs fully **before** upstream routing:
  authenticate → enabled/expiry → IP policy → rate → concurrency →
  token/budget → model/pool policy → routing → upstream.

## 7. Local API

- Default `http://127.0.0.1:<port>/v1`, loopback-only in v0.1.
- v0.1 compatibility target: model listing (`/v1/models`), Responses API
  (`/v1/responses`), and Chat Completions (`/v1/chat/completions`) surfaces
  required by common coding clients. Further routes are added explicitly;
  unknown `/v1/*` routes get a controlled unsupported-route response, never
  a blind pass-through. Route names are direction, not promises.
- Provider-aware and quota-aware deterministic routing:
  enabled → healthy → quota available → cooldown expired → session affinity
  (stable session identifier only) → priority/available quota → select.
  Safe retry and failover only (never after ambiguous sends or into an
  exposed stream), per-account cooldown; streaming passed through; every
  request accounted as a provider-neutral `RequestUsage` record.

## 8. API access management

- One `ApiClient` (e.g. a person, IDE setup, or automation) owns multiple
  `ApiKey`s for rotation (current + retiring).
- `AccessPolicy` per key or client: expiry, request rate, concurrency, token
  budgets (input/output/total), monetary budgets (estimated vs actual, only
  when the provider supplies cost), model allow/deny, pool/account
  restriction, IP allow/deny (never the sole boundary).
- `UsageRecord` aggregates from per-request `RequestUsage` rows.

## 9. Process model

One desktop process for v0.1: GUI webview, core services, gateway HTTP server,
quota refresher, and process manager all in-process. Closing the window
minimizes to tray; the process (and gateway) keeps running until quit. No
sidecar, daemon, or service — that complexity is deferred until a headless /
background use case forces it.

Selected stack (argued in the RFC): **Tauri 2 + Rust core + TypeScript web
frontend** — small footprint, Rust-native gateway/adapters/secret-store,
first-class tray/autostart/packaging, and the same shape as the closest
local-first reference.

## 10. Explicit non-goals for v0.1

Cloud control plane; hosted SaaS; Kubernetes / multi-node / distributed
routing; PostgreSQL server; Redis; billing platform and payment processing;
teams/organizations; own remote-relay infrastructure; mobile apps; LAN/remote
exposure (architecture reserves an `ApiExposure { Localhost, LAN,
RemoteTunnel }` switch for later — v0.1 is `Localhost` only); prompt/response
body storage.

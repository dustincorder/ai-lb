// Package providers will hold per-provider adapters (Codex, Antigravity,
// future providers) behind a provider-neutral contract: account lifecycle,
// subscription/quota mapping, credential activation, and client process
// control. No provider logic exists yet; the contract is defined in
// docs/RFC-0001-v0.1.md and must not be anticipated here with stubs that
// look like implementations.
package providers

// Package routing will hold deterministic, quota-aware account selection:
// candidate filtering (enabled, healthy, quota, cooldown), stable-session
// affinity, priority ordering, safe retry/failover invariants, and usage
// accounting hooks. No routing logic exists yet; the invariants are
// defined in docs/RFC-0001-v0.1.md.
package routing

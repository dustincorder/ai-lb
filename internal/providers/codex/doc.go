// Package codex integrates managed Codex accounts through the official
// `codex app-server` stdio protocol. Design rules (normative):
//
//   - Never read, parse, copy, or modify ~/.codex/auth.json or any
//     $CODEX_HOME/auth.json; never extract OAuth tokens from any keyring.
//   - No self-implemented ChatGPT OAuth, no undocumented backend calls,
//     no websocket transport, no experimentalApi flag.
//   - Only these stable methods are used: initialize (+ initialized
//     notification), account/read, account/login/start,
//     account/login/cancel, account/logout, account/rateLimits/read,
//     plus the account/login/completed, account/updated and
//     account/rateLimits/updated notifications.
//   - Upstream decoding is forward-compatible (unknown fields ignored);
//     our own management mutation payloads stay strict.
package codex

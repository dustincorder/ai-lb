// Package web embeds the production React build (web/dist) into the Go
// binary so the service ships as a single executable with no Node.js
// runtime on the user machine. Build the UI first:
//
//	npm --prefix web run build
//
// If dist is missing or has no index.html (e.g. Go tests, backend-only
// checkout), the control server answers / with 503 and an explanation.
package web

import "embed"

// Dist holds the production frontend build.

//go:embed all:dist
var Dist embed.FS

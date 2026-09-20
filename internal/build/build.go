// Package build carries typed build metadata injected at release time
// via ldflags (see .goreleaser.yml). Local development builds keep the
// dev defaults and must never behave like production releases — in
// particular, they skip automatic update checks.
package build

// Injected with:
//
//	-ldflags "-X github.com/dustincorder/ai-lb/internal/build.Version=...
//	          -X github.com/dustincorder/ai-lb/internal/build.Commit=...
//	          -X github.com/dustincorder/ai-lb/internal/build.BuildTime=...
//	          -X github.com/dustincorder/ai-lb/internal/build.Channel=..."
var (
	// Version is a release tag (stable: v1.2.3, nightly:
	// v1.2.3-abc1234-nightly) or "dev".
	Version = "dev"
	// Commit is the full git SHA the binary was built from.
	Commit = "none"
	// BuildTime is the RFC3339 build timestamp (empty for dev builds).
	BuildTime = ""
	// Channel is stable, nightly, or dev.
	Channel = "dev"
)

// Info is the typed build metadata snapshot.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	Channel   string `json:"channel"`
}

// Current returns the running binary's build metadata.
func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		Channel:   Channel,
	}
}

// IsDev reports whether this is a local development build. Dev builds
// skip automatic update checks; an explicit manual check is still allowed.
func (i Info) IsDev() bool {
	return i.Version == "dev" || i.Channel == "dev"
}

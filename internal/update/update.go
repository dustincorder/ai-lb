// Package update implements the foundation update checker: build
// channels (stable/nightly), GitHub Releases polling, version comparison,
// per-platform artifact selection, and verified downloads.
//
// Privacy: the checker issues plain HTTPS GETs to the public GitHub
// Releases API (and, on explicit user download, to release asset URLs)
// with a User-Agent of ai-lb/<version>. It sends no account data, API
// keys, provider information, quotas, machine IDs, usernames, or DB
// paths, and it never renders remote content inside the UI.
//
// Safety: checks never block startup, never touch the gateway path, and
// never fail the service. "Download update" only fetches and verifies an
// artifact; it never replaces the running binary or installs packages.
// Package-aware self-update is a separate future task.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/dustincorder/ai-lb/internal/build"
)

// Channels.
const (
	ChannelStable  = "stable"
	ChannelNightly = "nightly"
	ChannelDev     = "dev"
)

// Update statuses for GET /api/update.
const (
	StatusIdle      = "idle"
	StatusChecking  = "checking"
	StatusUpToDate  = "up_to_date"
	StatusAvailable = "available"
	StatusFailed    = "failed"
)

// checkTimeout bounds every update-check network round trip. A slow or
// unreachable GitHub must never stall the service or the UI action.
const checkTimeout = 10 * time.Second

// Asset is one GitHub release artifact.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release is a normalized GitHub release.
type Release struct {
	Tag         string
	Name        string
	URL         string
	Draft       bool
	Prerelease  bool
	PublishedAt time.Time
	Assets      []Asset
}

// Client polls the public GitHub Releases API. APIBase is overridable so
// tests run against a local server instead of the network.
type Client struct {
	APIBase string
	Owner   string
	Repo    string
	HTTP    *http.Client
	agent   string
}

// NewClient builds a Releases API client with a short timeout and an
// identifying User-Agent. No token is required or used.
func NewClient(owner, repo, version string) *Client {
	return &Client{
		APIBase: "https://api.github.com",
		Owner:   owner,
		Repo:    repo,
		HTTP:    &http.Client{Timeout: checkTimeout},
		agent:   "ai-lb/" + version,
	}
}

// rateLimitError marks GitHub throttling distinctly from other failures.
type rateLimitError struct {
	msg string
}

func (e *rateLimitError) Error() string { return e.msg }

// ListReleases fetches recent releases, newest first. Drafts are
// returned (callers filter them); malformed entries are skipped.
func (c *Client) ListReleases(ctx context.Context) ([]Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", c.APIBase, c.Owner, c.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.agent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		reset := resp.Header.Get("X-RateLimit-Reset")
		msg := fmt.Sprintf("GitHub API rate limited (HTTP %d)", resp.StatusCode)
		if reset != "" {
			msg += ", resets at unix " + reset
		}
		return nil, &rateLimitError{msg: msg}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	// Bound the API payload: releases metadata is small; anything huge
	// is rejected rather than buffered.
	limited := io.LimitReader(resp.Body, 4<<20)
	if err := json.NewDecoder(limited).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode GitHub releases: %w", err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.TagName == "" {
			continue
		}
		rel := Release{
			Tag:        r.TagName,
			Name:       r.Name,
			URL:        r.HTMLURL,
			Draft:      r.Draft,
			Prerelease: r.Prerelease,
		}
		if r.PublishedAt != "" {
			if ts, err := time.Parse(time.RFC3339, r.PublishedAt); err == nil {
				rel.PublishedAt = ts
			}
		}
		for _, a := range r.Assets {
			if a.Name == "" || a.BrowserDownloadURL == "" {
				continue
			}
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
		}
		out = append(out, rel)
	}
	return out, nil
}

// nightlyTag matches ai-lb nightly tags: v<base>-<sha>-nightly.
var nightlyTag = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)-([0-9a-f]{7,40})-nightly$`)

// stableTag matches plain releases: v<base>.
var stableTag = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

type parsedVersion struct {
	base    string // canonical semver (v-prefixed) for comparison
	nightly bool
	sha     string
}

// parseVersion classifies a tag. ok=false means the tag follows neither
// ai-lb convention and must be ignored (never offered, never fatal).
func parseVersion(tag string) (parsedVersion, bool) {
	if m := nightlyTag.FindStringSubmatch(tag); m != nil {
		base := "v" + m[1]
		if !semver.IsValid(base) {
			return parsedVersion{}, false
		}
		return parsedVersion{base: base, nightly: true, sha: m[2]}, true
	}
	if m := stableTag.FindStringSubmatch(tag); m != nil {
		base := "v" + m[1]
		if !semver.IsValid(base) {
			return parsedVersion{}, false
		}
		return parsedVersion{base: base}, true
	}
	return parsedVersion{}, false
}

// AssetInfo describes the selected download artifact, if any.
type AssetInfo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// State is the runtime update state served at GET /api/update. It never
// embeds raw GitHub JSON.
type State struct {
	Status           string     `json:"status"`
	Channel          string     `json:"channel"`
	CurrentVersion   string     `json:"current_version"`
	AvailableVersion string     `json:"available_version,omitempty"`
	AvailableChannel string     `json:"available_channel,omitempty"`
	ReleaseURL       string     `json:"release_url,omitempty"`
	Asset            *AssetInfo `json:"asset,omitempty"`
	AssetNote        string     `json:"asset_note,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	Error            string     `json:"error,omitempty"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
}

// Checker holds runtime update state and runs checks. Zero value is not
// usable; build with NewChecker.
type Checker struct {
	client    *Client
	goos      string
	goarch    string
	mu        sync.Mutex
	state     State
	now       func() time.Time
	buildTime func(build.Info) time.Time
}

// NewChecker builds a checker bound to the runtime platform.
func NewChecker(client *Client) *Checker {
	return &Checker{
		client: client,
		goos:   runtime.GOOS,
		goarch: runtime.GOARCH,
		state:  State{Status: StatusIdle},
		now:    time.Now,
		buildTime: func(b build.Info) time.Time {
			ts, _ := time.Parse(time.RFC3339, b.BuildTime)
			return ts
		},
	}
}

// newCheckerForTest overrides platform and clock.
func newCheckerForTest(client *Client, goos, goarch string, now time.Time) *Checker {
	k := NewChecker(client)
	k.goos = goos
	k.goarch = goarch
	k.now = func() time.Time { return now }
	return k
}

// Get returns a copy of the current state.
func (k *Checker) Get() State {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.state
}

// Check runs one update check synchronously and stores the result.
// Network/API failures become StatusFailed; they never propagate as
// service errors — callers treat a failed check as advisory.
func (k *Checker) Check(ctx context.Context, current build.Info, channel string) State {
	k.set(State{Status: StatusChecking, Channel: channel, CurrentVersion: current.Version})
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	rels, err := k.client.ListReleases(ctx)
	now := k.now()
	if err != nil {
		return k.set(State{
			Status: StatusFailed, Channel: channel, CurrentVersion: current.Version,
			Error: err.Error(), CheckedAt: &now,
		})
	}
	return k.set(k.evaluate(current, channel, rels, now))
}

func (k *Checker) set(s State) State {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.state = s
	return s
}

// latestPicks finds the newest stable release and the newest compatible
// nightly by publish time (never by string order). Drafts, wrongly named
// tags, and unparseable versions are ignored.
func latestPicks(rels []Release) (stable *Release, nightly *Release) {
	for i := range rels {
		r := &rels[i]
		if r.Draft {
			continue
		}
		pv, ok := parseVersion(r.Tag)
		if !ok {
			continue
		}
		_ = pv
		if r.Prerelease {
			// Only ai-lb nightly prereleases qualify; other prereleases
			// (betas, rcs) are never offered.
			if !pv.nightly {
				continue
			}
			if nightly == nil || r.PublishedAt.After(nightly.PublishedAt) {
				cp := *r
				nightly = &cp
			}
			continue
		}
		if pv.nightly {
			// A nightly-named tag published as a full release: treat by
			// name, not by flag — still a prerelease-shaped version.
			if nightly == nil || r.PublishedAt.After(nightly.PublishedAt) {
				cp := *r
				nightly = &cp
			}
			continue
		}
		if stable == nil || r.PublishedAt.After(stable.PublishedAt) {
			cp := *r
			stable = &cp
		}
	}
	return stable, nightly
}

// ArtifactSuffix is the platform suffix of release archives:
//
//	_<goos>_<goarch>.tar.gz (unix)
//	_<goos>_<goarch>.zip     (windows)
//
// Release builds exist for linux/amd64, linux/arm64, windows/amd64,
// windows/arm64, darwin/amd64, and darwin/arm64. The matching is generic
// over GOOS/GOARCH pairs on purpose: an unknown platform simply matches
// nothing (see SelectAsset), never some other platform's artifact.
func ArtifactSuffix(goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("_%s_%s.zip", goos, goarch)
	}
	return fmt.Sprintf("_%s_%s.tar.gz", goos, goarch)
}

// ArtifactName is the full release archive file name for a tag and
// platform: ai-lb_<version>_<goos>_<goarch>.<ext>. Tests and tooling
// must build expected names through this helper instead of hardcoding
// one platform, or matrix CI on other OSes breaks.
func ArtifactName(tag, goos, goarch string) string {
	return "ai-lb_" + tag + ArtifactSuffix(goos, goarch)
}

// SelectAsset picks the artifact for a platform from release naming.
// It matches by OS/arch suffix only, so version formats never matter.
// Nil means this release has no build for the platform.
func SelectAsset(rel *Release, goos, goarch string) *Asset {
	sfx := ArtifactSuffix(goos, goarch)
	for i := range rel.Assets {
		if strings.HasSuffix(rel.Assets[i].Name, sfx) {
			a := rel.Assets[i]
			return &a
		}
	}
	return nil
}

// withAsset attaches the platform artifact (or an explanatory note when
// the release carries no build for this platform).
func (k *Checker) withAsset(s State, rel *Release) State {
	if a := SelectAsset(rel, k.goos, k.goarch); a != nil {
		s.Asset = &AssetInfo{Name: a.Name, URL: a.URL, Size: a.Size}
	} else {
		s.AssetNote = fmt.Sprintf("no build for %s/%s in this release", k.goos, k.goarch)
	}
	ts := rel.PublishedAt
	s.PublishedAt = &ts
	return s
}

// evaluate implements the channel algorithms over fetched releases.
func (k *Checker) evaluate(current build.Info, channel string, rels []Release, now time.Time) State {
	cur, ok := parseVersion(current.Version)
	if !ok {
		return State{
			Status: StatusFailed, Channel: channel, CurrentVersion: current.Version,
			Error: fmt.Sprintf("current version %q is not a recognized release version", current.Version),
			CheckedAt: &now,
		}
	}
	stable, nightly := latestPicks(rels)
	switch channel {
	case ChannelNightly:
		return k.evaluateNightly(current, cur, stable, nightly, now)
	default:
		return k.evaluateStable(current, cur, stable, now)
	}
}

// evaluateStable only ever offers non-prerelease releases with a
// strictly newer base version (or, for a prerelease current build,
// the final release of its own base).
func (k *Checker) evaluateStable(current build.Info, cur parsedVersion, stable *Release, now time.Time) State {
	base := State{Status: StatusUpToDate, Channel: ChannelStable, CurrentVersion: current.Version, CheckedAt: &now}
	if stable == nil {
		return base
	}
	sv, _ := parseVersion(stable.Tag)
	cmp := semver.Compare(sv.base, cur.base)
	if cmp > 0 || (cmp == 0 && cur.nightly) {
		s := State{
			Status: StatusAvailable, Channel: ChannelStable, CurrentVersion: current.Version,
			AvailableVersion: stable.Tag, AvailableChannel: ChannelStable,
			ReleaseURL: stable.URL, CheckedAt: &now,
		}
		return k.withAsset(s, stable)
	}
	return base
}

// evaluateNightly offers the newest nightly, but prefers a final stable
// release at or above the current base (a final beats a prerelease of
// the same base). It never downgrades: older stables are ignored, and a
// nightly is offered only when it is identifiably newer.
func (k *Checker) evaluateNightly(current build.Info, cur parsedVersion, stable, nightly *Release, now time.Time) State {
	channel := ChannelNightly
	if stable != nil {
		sv, _ := parseVersion(stable.Tag)
		if semver.Compare(sv.base, cur.base) >= 0 {
			s := State{
				Status: StatusAvailable, Channel: channel, CurrentVersion: current.Version,
				AvailableVersion: stable.Tag, AvailableChannel: ChannelStable,
				ReleaseURL: stable.URL, CheckedAt: &now,
			}
			return k.withAsset(s, stable)
		}
	}
	if nightly != nil {
		nv, _ := parseVersion(nightly.Tag)
		if k.nightlyIsNewer(current, cur, nv, *nightly, stable) {
			s := State{
				Status: StatusAvailable, Channel: channel, CurrentVersion: current.Version,
				AvailableVersion: nightly.Tag, AvailableChannel: ChannelNightly,
				ReleaseURL: nightly.URL, CheckedAt: &now,
			}
			return k.withAsset(s, nightly)
		}
	}
	return State{Status: StatusUpToDate, Channel: channel, CurrentVersion: current.Version, CheckedAt: &now}
}

// nightlyIsNewer decides whether candidate N supersedes the current
// build without ever moving backwards.
func (k *Checker) nightlyIsNewer(current build.Info, cur parsedVersion, nv parsedVersion, n Release, stable *Release) bool {
	if cur.nightly {
		if nv.sha == cur.sha {
			return false
		}
		built := k.buildTime(current)
		if built.IsZero() {
			// No build timestamp to order by (should not happen for
			// real nightly builds, which always stamp one): a
			// different-SHA latest nightly is the best known newer
			// candidate.
			return true
		}
		return !n.PublishedAt.Before(built)
	}
	// Current is a final release: only nightlies at or above it qualify,
	// and only when published after the stable it would replace (or when
	// no stable exists to compare against).
	if semver.Compare(nv.base, cur.base) < 0 {
		return false
	}
	if stable == nil {
		return true
	}
	return n.PublishedAt.After(stable.PublishedAt)
}

// DownloadResult is a verified artifact fetch. Verification (SHA-256
// against the release checksums.txt) must pass before the result is
// returned; nothing is installed.
type DownloadResult struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Download re-evaluates, then streams the selected asset to a temp file
// while hashing, and accepts it only on checksum match.
func (k *Checker) Download(ctx context.Context, current build.Info, channel string) (DownloadResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rels, err := k.client.ListReleases(ctx)
	if err != nil {
		return DownloadResult{}, err
	}
	now := k.now()
	st := k.evaluate(current, channel, rels, now)
	if st.Status != StatusAvailable || st.Asset == nil {
		return DownloadResult{}, fmt.Errorf("no downloadable update available")
	}
	rel := k.findRelease(rels, st.AvailableVersion)
	if rel == nil {
		return DownloadResult{}, fmt.Errorf("release %q disappeared", st.AvailableVersion)
	}
	sums := k.findChecksums(rel)
	if sums == nil {
		return DownloadResult{}, fmt.Errorf("release %q has no checksums.txt", rel.Tag)
	}
	want, err := k.checksumFor(ctx, sums, st.Asset.Name)
	if err != nil {
		return DownloadResult{}, err
	}
	return k.fetchVerified(ctx, st.Asset, want)
}

func (k *Checker) findRelease(rels []Release, tag string) *Release {
	for i := range rels {
		if rels[i].Tag == tag {
			return &rels[i]
		}
	}
	return nil
}

func (k *Checker) findChecksums(rel *Release) *Asset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == "checksums.txt" {
			a := rel.Assets[i]
			return &a
		}
	}
	return nil
}

// checksumFor downloads checksums.txt and extracts the expected hex hash
// for one artifact name.
func (k *Checker) checksumFor(ctx context.Context, sums *Asset, name string) (string, error) {
	data, err := k.getBytes(ctx, sums.URL, 1<<20)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			sum := strings.ToLower(fields[0])
			if len(sum) != 64 {
				continue
			}
			if _, err := hex.DecodeString(sum); err != nil {
				continue
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("no checksum for %q", name)
}

// fetchVerified streams the asset to a temp file and accepts it only on
// hash match. The file is left in place for the user; cleanup of stale
// downloads is the operator's job, not the service's.
func (k *Checker) fetchVerified(ctx context.Context, asset *AssetInfo, want string) (DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return DownloadResult{}, err
	}
	req.Header.Set("User-Agent", k.client.agent)
	resp, err := k.client.HTTP.Do(req)
	if err != nil {
		return DownloadResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	dir, err := os.MkdirTemp("", "ai-lb-update-*")
	if err != nil {
		return DownloadResult{}, err
	}
	path := filepath.Join(dir, asset.Name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return DownloadResult{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hash), resp.Body); err != nil {
		f.Close()
		os.Remove(path)
		return DownloadResult{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return DownloadResult{}, err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		os.Remove(path)
		return DownloadResult{}, fmt.Errorf("checksum mismatch for %q", asset.Name)
	}
	var size int64
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	return DownloadResult{Path: path, SHA256: got, Size: size}, nil
}

func (k *Checker) getBytes(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", k.client.agent)
	resp, err := k.client.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch returned HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

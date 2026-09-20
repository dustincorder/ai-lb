package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dustincorder/ai-lb/internal/build"
)

var testNow = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

type apiAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type apiRelease struct {
	Tag         string     `json:"tag_name"`
	Name        string     `json:"name"`
	URL         string     `json:"html_url"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt string     `json:"published_at"`
	Assets      []apiAsset `json:"assets"`
}

func assetFor(tag, goos, goarch string) apiAsset {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	name := "ai-lb_" + tag + "_" + goos + "_" + goarch + "." + ext
	return apiAsset{Name: name, URL: "https://example.com/" + name, Size: 42}
}

func mkRelease(tag string, prerelease, draft bool, published time.Time, assets ...apiAsset) apiRelease {
	return apiRelease{
		Tag: tag, Name: tag, URL: "https://example.com/releases/" + tag,
		Draft: draft, Prerelease: prerelease,
		PublishedAt: published.Format(time.RFC3339), Assets: assets,
	}
}

// serveReleases returns a client pointed at a test server answering the
// releases list, plus the server for asset/download tests.
func serveReleases(t *testing.T, rels []apiRelease, muxExtra map[string]http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rels)
	})
	for pattern, h := range muxExtra {
		mux.HandleFunc(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{APIBase: srv.URL, Owner: "o", Repo: "r", HTTP: srv.Client(), agent: "ai-lb/test"}, srv
}

func checkerAt(client *Client) *Checker {
	return newCheckerForTest(client, "linux", "amd64", testNow)
}

func stableBuild(v string) build.Info {
	return build.Info{Version: v, Commit: "c", Channel: ChannelStable}
}

func nightlyBuild(tag, sha string) build.Info {
	return build.Info{
		Version: tag, Commit: sha, Channel: ChannelNightly,
		BuildTime: testNow.Add(-time.Hour).Format(time.RFC3339),
	}
}

func TestStableDetectsNewerStable(t *testing.T) {
	rels := []apiRelease{mkRelease("v0.2.0", false, false, testNow, assetFor("v0.2.0", "linux", "amd64"))}
	client, _ := serveReleases(t, rels, nil)
	st := checkerAt(client).Check(context.Background(), stableBuild("v0.1.0"), ChannelStable)
	if st.Status != StatusAvailable {
		t.Fatalf("status = %q, want available", st.Status)
	}
	if st.AvailableVersion != "v0.2.0" || st.AvailableChannel != ChannelStable {
		t.Errorf("unexpected offer: %+v", st)
	}
	if st.Asset == nil || st.ReleaseURL == "" {
		t.Errorf("available state must name asset and release URL: %+v", st)
	}
}

func TestStableIgnoresPrereleaseSameOrNewer(t *testing.T) {
	rels := []apiRelease{
		mkRelease("v0.2.0-beta.1", true, false, testNow, assetFor("v0.2.0-beta.1", "linux", "amd64")),
		mkRelease("v0.2.0-abc1234-nightly", true, false, testNow, assetFor("v0.2.0-abc1234-nightly", "linux", "amd64")),
		mkRelease("v0.1.0", false, false, testNow.Add(-time.Hour)),
	}
	client, _ := serveReleases(t, rels, nil)
	st := checkerAt(client).Check(context.Background(), stableBuild("v0.1.0"), ChannelStable)
	if st.Status != StatusUpToDate {
		t.Errorf("stable must ignore prereleases, got %+v", st)
	}
}

func TestStableIgnoresSameAndOlder(t *testing.T) {
	rels := []apiRelease{
		mkRelease("v0.1.0", false, false, testNow),
		mkRelease("v0.0.9", false, false, testNow.Add(-time.Hour)),
	}
	client, _ := serveReleases(t, rels, nil)
	for _, cur := range []string{"v0.1.0", "v0.2.0"} {
		st := checkerAt(client).Check(context.Background(), stableBuild(cur), ChannelStable)
		if st.Status != StatusUpToDate {
			t.Errorf("current %s: status = %q, want up_to_date", cur, st.Status)
		}
	}
}

func TestNightlyDetectsNewerNightly(t *testing.T) {
	rels := []apiRelease{
		mkRelease("v0.2.0-def5678-nightly", true, false, testNow,
			assetFor("v0.2.0-def5678-nightly", "linux", "amd64")),
		mkRelease("v0.1.0", false, false, testNow.Add(-48*time.Hour)),
	}
	client, _ := serveReleases(t, rels, nil)
	st := checkerAt(client).Check(context.Background(),
		nightlyBuild("v0.2.0-abc1234-nightly", "abc1234"), ChannelNightly)
	if st.Status != StatusAvailable || st.AvailableChannel != ChannelNightly {
		t.Fatalf("should offer newer nightly, got %+v", st)
	}
	if st.AvailableVersion != "v0.2.0-def5678-nightly" {
		t.Errorf("wrong nightly offered: %+v", st)
	}
}

func TestNightlyPrefersFinalStableOverSameBase(t *testing.T) {
	// Current nightly on base 0.2.0; final v0.2.0 appears: offer stable,
	// even though a newer nightly also exists.
	rels := []apiRelease{
		mkRelease("v0.2.0-zzz9999-nightly", true, false, testNow),
		mkRelease("v0.2.0", false, false, testNow.Add(-time.Hour)),
	}
	client, _ := serveReleases(t, rels, nil)
	st := checkerAt(client).Check(context.Background(),
		nightlyBuild("v0.2.0-abc1234-nightly", "abc1234"), ChannelNightly)
	if st.Status != StatusAvailable {
		t.Fatalf("status = %q, want available", st.Status)
	}
	if st.AvailableVersion != "v0.2.0" || st.AvailableChannel != ChannelStable {
		t.Errorf("final stable must beat same-base prerelease, got %+v", st)
	}
}

func TestNightlyNeverDowngradesToOlderStable(t *testing.T) {
	// Stable v0.1.0 is older than the nightly base v0.2.0; a newer
	// nightly exists → offer the nightly, never the older stable.
	rels := []apiRelease{
		mkRelease("v0.2.0-def5678-nightly", true, false, testNow,
			assetFor("v0.2.0-def5678-nightly", "linux", "amd64")),
		mkRelease("v0.1.0", false, false, testNow.Add(-time.Hour)),
	}
	client, _ := serveReleases(t, rels, nil)
	st := checkerAt(client).Check(context.Background(),
		nightlyBuild("v0.2.0-abc1234-nightly", "abc1234"), ChannelNightly)
	if st.Status != StatusAvailable || st.AvailableChannel != ChannelNightly {
		t.Errorf("must offer newer nightly, not older stable: %+v", st)
	}
}

func TestDraftsAndWrongNamesIgnored(t *testing.T) {
	rels := []apiRelease{
		mkRelease("v9.9.9", false, true, testNow),                        // draft: ignore
		mkRelease("nightly-latest", true, false, testNow),                // wrong name: ignore
		mkRelease("v0.2.0-XYZ-nightly", true, false, testNow),            // bad sha: ignore
		mkRelease("v0.1.0", false, false, testNow.Add(-time.Hour)),       // real stable
	}
	client, _ := serveReleases(t, rels, nil)
	st := checkerAt(client).Check(context.Background(), stableBuild("v0.1.0"), ChannelStable)
	if st.Status != StatusUpToDate {
		t.Errorf("drafts and misnamed tags must be ignored, got %+v", st)
	}
}

func TestUnsupportedArchitectureHandled(t *testing.T) {
	rels := []apiRelease{mkRelease("v0.2.0", false, false, testNow, assetFor("v0.2.0", "linux", "amd64"))}
	client, _ := serveReleases(t, rels, nil)
	k := newCheckerForTest(client, "linux", "riscv64", testNow)
	st := k.Check(context.Background(), stableBuild("v0.1.0"), ChannelStable)
	if st.Status != StatusAvailable {
		t.Fatalf("update exists even without a local artifact, got %+v", st)
	}
	if st.Asset != nil || st.AssetNote == "" {
		t.Errorf("missing artifact must leave Asset empty with a note, got %+v", st)
	}
}

func TestTimeoutIsNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)
	client := &Client{
		APIBase: srv.URL, Owner: "o", Repo: "r",
		HTTP:    &http.Client{Timeout: 50 * time.Millisecond},
		agent:   "ai-lb/test",
	}
	st := checkerAt(client).Check(context.Background(), stableBuild("v0.1.0"), ChannelStable)
	if st.Status != StatusFailed || st.Error == "" {
		t.Errorf("timeout must be a failed state, not fatal: %+v", st)
	}
}

func TestRateLimitRepresentedAsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Reset", "1234567890")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	client := &Client{APIBase: srv.URL, Owner: "o", Repo: "r", HTTP: srv.Client(), agent: "ai-lb/test"}
	st := checkerAt(client).Check(context.Background(), stableBuild("v0.1.0"), ChannelStable)
	if st.Status != StatusFailed {
		t.Fatalf("rate limit must be failed state, got %+v", st)
	}
	if got := st.Error; got == "" {
		t.Error("rate limit must surface an explanatory error")
	}
}

func TestDownloadSuccessAndMismatch(t *testing.T) {
	payload := []byte("fake-binary-bytes")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	tag := "v0.2.0"

	var muxURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		rel := mkRelease(tag, false, false, testNow,
			apiAsset{Name: "ai-lb_" + tag + "_linux_amd64.tar.gz", URL: muxURL + "/dl/bin", Size: int64(len(payload))},
			apiAsset{Name: "checksums.txt", URL: muxURL + "/dl/sums", Size: 200},
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]apiRelease{rel})
	})
	bad := false
	mux.HandleFunc("/dl/bin", func(w http.ResponseWriter, r *http.Request) {
		if bad {
			_, _ = w.Write([]byte("tampered"))
			return
		}
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(hexSum + "  ai-lb_" + tag + "_linux_amd64.tar.gz\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	muxURL = srv.URL

	client := &Client{APIBase: srv.URL, Owner: "o", Repo: "r", HTTP: srv.Client(), agent: "ai-lb/test"}
	k := checkerAt(client)
	cur := stableBuild("v0.1.0")

	res, err := k.Download(context.Background(), cur, ChannelStable)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if string(data) != string(payload) {
		t.Error("downloaded bytes differ")
	}
	if res.SHA256 != hexSum {
		t.Errorf("sha = %q, want %q", res.SHA256, hexSum)
	}
	if filepath.Base(filepath.Dir(res.Path)) == "" {
		t.Error("expected temp dir path")
	}

	bad = true
	if _, err := k.Download(context.Background(), cur, ChannelStable); err == nil {
		t.Error("tampered download must be rejected on checksum mismatch")
	} else if got := err.Error(); got != `checksum mismatch for "ai-lb_v0.2.0_linux_amd64.tar.gz"` {
		t.Errorf("unexpected mismatch error: %v", err)
	}
}

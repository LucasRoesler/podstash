package podstash

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testApp(t *testing.T) (*App, string) {
	t.Helper()
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, podcastsDir), 0755)

	return &App{
		DataDir: dataDir,
		Client:  http.DefaultClient,
		Tmpl:    loadTemplates(),
	}, dataDir
}

func seedPodcast(t *testing.T, dataDir, slug string) {
	t.Helper()
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)
	SaveMeta(dir, &PodcastMeta{
		FeedURL: "https://example.com/" + slug,
		Title:   slug,
		Author:  "Author",
		AddedAt: time.Now().UTC(),
		Slug:    slug,
	})
	SaveIndex(dir, &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{GUID: "ep1", Title: "First", Filename: "first.mp3", FileSize: 1000},
			{GUID: "ep2", Title: "Second"},
		},
	})
}

func TestHandleHome(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "test-pod")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	app.handleHome(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "test-pod") {
		t.Error("response should contain podcast name")
	}
	if !strings.Contains(body, "1 / 2 episodes") {
		t.Errorf("response should show episode counts, got: %s", body)
	}
}

func TestHandleHomeEmpty(t *testing.T) {
	app, _ := testApp(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	app.handleHome(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No podcasts") {
		t.Error("should show empty state message")
	}
}

func TestHandlePodcastDetail(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "my-podcast")

	req := httptest.NewRequest("GET", "/podcasts/my-podcast", nil)
	req.SetPathValue("slug", "my-podcast")
	w := httptest.NewRecorder()
	app.handlePodcast(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "my-podcast") {
		t.Error("should contain podcast title")
	}
	if !strings.Contains(body, "First") {
		t.Error("should contain episode title")
	}
}

func TestHandlePodcastDetailEpisodeOrder(t *testing.T) {
	app, dataDir := testApp(t)
	dir := PodcastDir(dataDir, "ordered")
	os.MkdirAll(dir, 0755)
	SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/ordered", Title: "Ordered", Slug: "ordered", AddedAt: time.Now().UTC()})
	SaveIndex(dir, &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{GUID: "old", Title: "Oldest", PubDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
			{GUID: "new", Title: "Newest", PubDate: time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)},
			{GUID: "mid", Title: "Middle", PubDate: time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)},
		},
	})

	req := httptest.NewRequest("GET", "/podcasts/ordered", nil)
	req.SetPathValue("slug", "ordered")
	w := httptest.NewRecorder()
	app.handlePodcast(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	newestIdx := strings.Index(body, "Newest")
	middleIdx := strings.Index(body, "Middle")
	oldestIdx := strings.Index(body, "Oldest")
	if newestIdx < 0 || middleIdx < 0 || oldestIdx < 0 {
		t.Fatalf("missing episode titles in body: newest=%d middle=%d oldest=%d", newestIdx, middleIdx, oldestIdx)
	}
	if !(newestIdx < middleIdx && middleIdx < oldestIdx) {
		t.Errorf("episodes not sorted by pub_date desc: newest=%d middle=%d oldest=%d", newestIdx, middleIdx, oldestIdx)
	}
}

func TestHandlePodcastDetailNotFound(t *testing.T) {
	app, _ := testApp(t)

	req := httptest.NewRequest("GET", "/podcasts/nonexistent", nil)
	req.SetPathValue("slug", "nonexistent")
	w := httptest.NewRecorder()
	app.handlePodcast(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleAddPage(t *testing.T) {
	app, _ := testApp(t)

	req := httptest.NewRequest("GET", "/add", nil)
	w := httptest.NewRecorder()
	app.handleAddPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Feed URL") {
		t.Error("should contain feed URL form")
	}
	if !strings.Contains(body, "OPML") {
		t.Error("should contain OPML upload form")
	}
}

func TestHandleAddPodcast(t *testing.T) {
	feedXML, _ := os.ReadFile("testdata/feed_simple.xml")
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(feedXML)
	}))
	defer feedSrv.Close()

	app, dataDir := testApp(t)
	app.Client = feedSrv.Client()

	form := url.Values{"url": {feedSrv.URL}}
	req := httptest.NewRequest("POST", "/podcasts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	app.handleAddPodcast(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	slug := Slugify("Test Podcast")
	if loc := w.Header().Get("Location"); loc != "/podcasts/"+slug {
		t.Errorf("Location = %q, want %q", loc, "/podcasts/"+slug)
	}

	// Verify podcast was created on disk.
	dir := PodcastDir(dataDir, slug)
	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("podcast not created: %v", err)
	}
	if meta.Title != "Test Podcast" {
		t.Errorf("Title = %q", meta.Title)
	}
}

func TestHandleDeletePodcast(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "to-delete")

	req := httptest.NewRequest("POST", "/podcasts/to-delete/delete", nil)
	req.SetPathValue("slug", "to-delete")
	w := httptest.NewRecorder()
	app.handleDeletePodcast(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	dir := PodcastDir(dataDir, "to-delete")
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("podcast directory should be deleted")
	}
}

func TestHandlePausePodcast(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "to-pause")

	req := httptest.NewRequest("POST", "/podcasts/to-pause/pause", nil)
	req.SetPathValue("slug", "to-pause")
	req.Header.Set("Referer", "/")
	w := httptest.NewRecorder()
	app.handlePausePodcast(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}

	dir := PodcastDir(dataDir, "to-pause")
	meta, _ := LoadMeta(dir)
	if !meta.Paused {
		t.Error("podcast should be paused")
	}

	// Toggle again to unpause.
	req = httptest.NewRequest("POST", "/podcasts/to-pause/pause", nil)
	req.SetPathValue("slug", "to-pause")
	w = httptest.NewRecorder()
	app.handlePausePodcast(w, req)

	meta, _ = LoadMeta(dir)
	if meta.Paused {
		t.Error("podcast should be unpaused")
	}
}

func TestHandleAddSkipPattern(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "skip-pod")

	form := url.Values{"pattern": {`(?i)best\s+of`}}
	req := httptest.NewRequest("POST", "/podcasts/skip-pod/skip", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", "skip-pod")
	w := httptest.NewRecorder()
	app.handleAddSkipPattern(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/podcasts/skip-pod" {
		t.Errorf("Location = %q, want %q", loc, "/podcasts/skip-pod")
	}

	dir := PodcastDir(dataDir, "skip-pod")
	meta, _ := LoadMeta(dir)
	if len(meta.SkipPatterns) != 1 {
		t.Fatalf("got %d skip patterns, want 1", len(meta.SkipPatterns))
	}
	if meta.SkipPatterns[0] != `(?i)best\s+of` {
		t.Errorf("pattern = %q", meta.SkipPatterns[0])
	}
}

func TestHandleAddSkipPatternInvalidRegex(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "skip-bad")

	form := url.Values{"pattern": {`[invalid`}}
	req := httptest.NewRequest("POST", "/podcasts/skip-bad/skip", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", "skip-bad")
	w := httptest.NewRecorder()
	app.handleAddSkipPattern(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid regex", w.Code)
	}
}

func TestHandleDeleteSkipPattern(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "skip-del")

	// Add two patterns first.
	dir := PodcastDir(dataDir, "skip-del")
	meta, _ := LoadMeta(dir)
	meta.SkipPatterns = []string{`pattern-a`, `pattern-b`}
	SaveMeta(dir, meta)

	// Delete index 0.
	form := url.Values{"index": {"0"}}
	req := httptest.NewRequest("POST", "/podcasts/skip-del/skip/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", "skip-del")
	w := httptest.NewRecorder()
	app.handleDeleteSkipPattern(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/podcasts/skip-del" {
		t.Errorf("Location = %q, want %q", loc, "/podcasts/skip-del")
	}

	updated, _ := LoadMeta(dir)
	if len(updated.SkipPatterns) != 1 {
		t.Fatalf("got %d patterns, want 1", len(updated.SkipPatterns))
	}
	if updated.SkipPatterns[0] != "pattern-b" {
		t.Errorf("remaining pattern = %q, want pattern-b", updated.SkipPatterns[0])
	}
}

func TestHandleOPMLImport(t *testing.T) {
	feedXML, _ := os.ReadFile("testdata/feed_simple.xml")
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(feedXML)
	}))
	defer feedSrv.Close()

	app, dataDir := testApp(t)
	app.Client = feedSrv.Client()

	// Create OPML content pointing to our test server.
	opmlContent := `<?xml version="1.0"?>
<opml version="2.0">
  <body>
    <outline type="rss" text="Test" xmlUrl="` + feedSrv.URL + `"/>
  </body>
</opml>`

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("opml", "import.opml")
	part.Write([]byte(opmlContent))
	writer.Close()

	req := httptest.NewRequest("POST", "/opml", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	app.handleOPMLImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "Imported 1") {
		t.Errorf("expected success message, got: %s", respBody)
	}

	// Verify podcast was created.
	podcasts, _ := ListPodcasts(dataDir)
	if len(podcasts) != 1 {
		t.Errorf("got %d podcasts, want 1", len(podcasts))
	}
}

func TestHandlePodcastFeed(t *testing.T) {
	app, dataDir := testApp(t)
	slug := "feed-test"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)
	SaveMeta(dir, &PodcastMeta{
		FeedURL: "https://example.com/feed",
		Title:   "Feed Test",
		Slug:    slug,
		AddedAt: time.Now().UTC(),
	})
	SaveIndex(dir, &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{GUID: "ep1", Title: "Episode One", Filename: "ep1.mp3", FileSize: 1000, EnclosureType: "audio/mpeg", PubDate: time.Now().UTC()},
			{GUID: "ep2", Title: "Not Downloaded"}, // no filename
		},
	})

	req := httptest.NewRequest("GET", "/podcasts/feed-test/feed.xml", nil)
	req.SetPathValue("slug", slug)
	w := httptest.NewRecorder()
	app.handlePodcastFeed(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/rss+xml") {
		t.Errorf("Content-Type = %q, want rss+xml", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Episode One") {
		t.Error("feed should contain downloaded episode")
	}
	if strings.Contains(body, "Not Downloaded") {
		t.Error("feed should not contain episode without local file")
	}
	if !strings.Contains(body, "/podcasts/feed-test/episodes/ep1.mp3") {
		t.Error("feed should contain local episode URL")
	}
	if !strings.Contains(body, "/podcasts/feed-test/cover.jpg") {
		t.Error("feed should contain local cover image URL")
	}
}

func TestHandlePodcastFeedNotFound(t *testing.T) {
	app, _ := testApp(t)

	req := httptest.NewRequest("GET", "/podcasts/nonexistent/feed.xml", nil)
	req.SetPathValue("slug", "nonexistent")
	w := httptest.NewRecorder()
	app.handlePodcastFeed(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleServeEpisode(t *testing.T) {
	app, dataDir := testApp(t)
	slug := "serve-test"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)
	SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/feed", Title: slug, Slug: slug})

	// Write a dummy file to serve.
	episodeFile := filepath.Join(dir, "ep1.mp3")
	os.WriteFile(episodeFile, []byte("fake audio data"), 0644)

	req := httptest.NewRequest("GET", "/podcasts/serve-test/episodes/ep1.mp3", nil)
	req.SetPathValue("slug", slug)
	req.SetPathValue("filename", "ep1.mp3")
	w := httptest.NewRecorder()
	app.handleServeEpisode(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Body.String() != "fake audio data" {
		t.Errorf("body = %q, want %q", w.Body.String(), "fake audio data")
	}
}

func TestHandleServeEpisodeForbiddenFilenames(t *testing.T) {
	app, _ := testApp(t)

	tests := []string{
		"../secret",            // path traversal
		"../../etc/passwd",     // path traversal
		"a/b",                  // directory component
		".podstash.meta.json",  // internal metadata file (non-audio extension)
		".podstash.index.json", // internal index file (non-audio extension)
		"cover.jpg",            // non-audio extension
	}
	for _, filename := range tests {
		req := httptest.NewRequest("GET", "/podcasts/pod/episodes/"+filename, nil)
		req.SetPathValue("slug", "pod")
		req.SetPathValue("filename", filename)
		w := httptest.NewRecorder()
		app.handleServeEpisode(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("filename %q: status = %d, want 404", filename, w.Code)
		}
	}
}

func TestHandleServeEpisodeNotFound(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "serve-missing")

	req := httptest.NewRequest("GET", "/podcasts/serve-missing/episodes/missing.mp3", nil)
	req.SetPathValue("slug", "serve-missing")
	req.SetPathValue("filename", "missing.mp3")
	w := httptest.NewRecorder()
	app.handleServeEpisode(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleServeCover(t *testing.T) {
	app, dataDir := testApp(t)
	slug := "cover-test"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)
	SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/feed", Title: slug, Slug: slug})

	// Write a fake cover image.
	coverData := []byte("fake jpeg data")
	os.WriteFile(filepath.Join(dir, coverFilename), coverData, 0644)

	req := httptest.NewRequest("GET", "/podcasts/cover-test/cover.jpg", nil)
	req.SetPathValue("slug", slug)
	w := httptest.NewRecorder()
	app.handleServeCover(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != string(coverData) {
		t.Errorf("body = %q, want %q", w.Body.String(), coverData)
	}
}

func TestHandleServeCoverNotFound(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "no-cover")

	req := httptest.NewRequest("GET", "/podcasts/no-cover/cover.jpg", nil)
	req.SetPathValue("slug", "no-cover")
	w := httptest.NewRecorder()
	app.handleServeCover(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleOPMLExport(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "export-test")

	req := httptest.NewRequest("GET", "/opml", nil)
	w := httptest.NewRecorder()
	app.handleOPMLExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/xml" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "export-test") {
		t.Error("OPML should contain podcast feed URL")
	}
}

func TestSlugParamRejectsTraversal(t *testing.T) {
	app, _ := testApp(t)

	maliciousSlugs := []string{"../etc", "..", ".", "foo/bar", "foo\\bar"}
	for _, slug := range maliciousSlugs {
		t.Run(slug, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/podcasts/"+slug, nil)
			req.SetPathValue("slug", slug)
			w := httptest.NewRecorder()
			app.handlePodcast(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("handlePodcast(%q): status = %d, want 404", slug, w.Code)
			}
		})
	}

	// Also verify delete handler rejects traversal.
	for _, slug := range maliciousSlugs {
		t.Run("delete/"+slug, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/podcasts/"+slug+"/delete", nil)
			req.SetPathValue("slug", slug)
			w := httptest.NewRecorder()
			app.handleDeletePodcast(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("handleDeletePodcast(%q): status = %d, want 404", slug, w.Code)
			}
		})
	}
}

func TestHandleRefreshPodcast(t *testing.T) {
	feedXML, _ := os.ReadFile("testdata/feed_simple.xml")
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(feedXML)
	}))
	defer feedSrv.Close()

	app, dataDir := testApp(t)
	app.Client = feedSrv.Client()
	seedPodcast(t, dataDir, "refresh-me")

	// Update the feed URL to point to our test server.
	dir := PodcastDir(dataDir, "refresh-me")
	meta, _ := LoadMeta(dir)
	meta.FeedURL = feedSrv.URL
	SaveMeta(dir, meta)

	req := httptest.NewRequest("POST", "/podcasts/refresh-me/refresh", nil)
	req.SetPathValue("slug", "refresh-me")
	req.Header.Set("Referer", "/podcasts/refresh-me")
	w := httptest.NewRecorder()
	app.handleRefreshPodcast(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/podcasts/refresh-me" {
		t.Errorf("Location = %q, want %q", loc, "/podcasts/refresh-me")
	}
}

func TestHandleRefreshPodcastNotFound(t *testing.T) {
	app, _ := testApp(t)

	req := httptest.NewRequest("POST", "/podcasts/nonexistent/refresh", nil)
	req.SetPathValue("slug", "nonexistent")
	w := httptest.NewRecorder()
	app.handleRefreshPodcast(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleSetDownloadAfter(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "date-pod")

	// Set a download-after date.
	form := url.Values{"date": {"2025-06-01"}}
	req := httptest.NewRequest("POST", "/podcasts/date-pod/download-after", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", "date-pod")
	w := httptest.NewRecorder()
	app.handleSetDownloadAfter(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	dir := PodcastDir(dataDir, "date-pod")
	meta, _ := LoadMeta(dir)
	if meta.DownloadAfter == nil {
		t.Fatal("DownloadAfter should be set")
	}
	want := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if !meta.DownloadAfter.Equal(want) {
		t.Errorf("DownloadAfter = %v, want %v", meta.DownloadAfter, want)
	}

	// Clear the date.
	form = url.Values{"clear": {"1"}}
	req = httptest.NewRequest("POST", "/podcasts/date-pod/download-after", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", "date-pod")
	w = httptest.NewRecorder()
	app.handleSetDownloadAfter(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("clear: status = %d, want 303", w.Code)
	}

	meta, _ = LoadMeta(dir)
	if meta.DownloadAfter != nil {
		t.Errorf("DownloadAfter should be nil after clear, got %v", meta.DownloadAfter)
	}
}

func TestHandleSetDownloadAfterInvalidDate(t *testing.T) {
	app, dataDir := testApp(t)
	seedPodcast(t, dataDir, "bad-date")

	form := url.Values{"date": {"not-a-date"}}
	req := httptest.NewRequest("POST", "/podcasts/bad-date/download-after", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", "bad-date")
	w := httptest.NewRecorder()
	app.handleSetDownloadAfter(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleHealthz(t *testing.T) {
	app, _ := testApp(t)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	app.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestHandleHealthzMissingDir(t *testing.T) {
	app := &App{
		DataDir: "/nonexistent/path/that/does/not/exist",
		Tmpl:    loadTemplates(),
	}

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	app.handleHealthz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// Regression test for issue #8: the healthcheck must not write to the data
// directory. A probe file's create and unlink bump the directory mtime, forcing
// a journal commit that keeps spinning disks awake between polls.
func TestHandleHealthzDoesNotWriteToDataDir(t *testing.T) {
	app, dir := testApp(t)
	podcasts := filepath.Join(dir, podcastsDir)

	// Seed a file so the assertion also catches a handler that rewrites an
	// existing path: that leaves the directory mtime alone but changes the
	// file's identity.
	seeded := filepath.Join(podcasts, "seed.json")
	if err := os.WriteFile(seeded, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	seedBefore := fileIdentity(t, seeded)

	before, err := os.ReadDir(podcasts)
	if err != nil {
		t.Fatalf("read podcasts dir: %v", err)
	}

	// Directory mtime is the only signal that catches a probe file created and
	// unlinked inside the handler: the listing looks identical afterwards, yet
	// each entry change bumps the mtime and forces a journal commit, which is
	// the behaviour issue #8 reported. The sleep clears the filesystem's
	// timestamp granularity so that bump is visible.
	dirBefore, err := os.Stat(podcasts)
	if err != nil {
		t.Fatalf("stat podcasts dir: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	app.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	after, err := os.ReadDir(podcasts)
	if err != nil {
		t.Fatalf("read podcasts dir: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("podcasts dir entries changed: %d -> %d, healthcheck must not write",
			len(before), len(after))
	}
	if seedAfter := fileIdentity(t, seeded); seedAfter != seedBefore {
		t.Errorf("existing file rewritten: inode %d -> %d, healthcheck must not write",
			seedBefore, seedAfter)
	}

	dirAfter, err := os.Stat(podcasts)
	if err != nil {
		t.Fatalf("stat podcasts dir: %v", err)
	}
	if !dirAfter.ModTime().Equal(dirBefore.ModTime()) {
		t.Errorf("podcasts dir mtime changed: %v -> %v, healthcheck must not write",
			dirBefore.ModTime(), dirAfter.ModTime())
	}
}

func TestHandleHealthzReadOnlyDataDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	dir := readOnlyPodcastsDir(t)
	app := &App{DataDir: dir, Tmpl: loadTemplates()}

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	app.handleHealthz(w, req)

	// Writability is a startup concern; a readable directory stays healthy.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// Before the first poll after a restart there is no in-memory poll time, so the
// home page falls back to the persisted last-change time and labels it as such.
func TestHandleHomeFallsBackToLastChangedAt(t *testing.T) {
	app, dataDir := testApp(t)
	app.Heartbeat = NewPollHeartbeat()

	slug := "show"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	changed := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/f.xml", Title: "Show", LastChangedAt: changed}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	// No poll recorded yet: the change time is shown, marked as not polled.
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	app.handleHome(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "updated") {
		t.Errorf("home page did not label the fallback as an update time:\n%s", body)
	}

	// After a poll, the live time is shown instead.
	polled := time.Now().UTC()
	app.Heartbeat.Mark(slug, polled)

	w = httptest.NewRecorder()
	app.handleHome(w, req)
	if body := w.Body.String(); !strings.Contains(body, "checked") {
		t.Errorf("home page did not show the poll time after a poll:\n%s", body)
	}
}

// The home page label has four states, and the two zero-time ones must render
// no label and no orphaned separator.
func TestHomeTemplateActivityLabelStates(t *testing.T) {
	when := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		view        PodcastView
		wantChecked bool
		wantUpdated bool
	}{
		{
			name:        "polled with a time",
			view:        PodcastView{Meta: PodcastMeta{Title: "A"}, LastActivity: when, Polled: true},
			wantChecked: true,
		},
		{
			name:        "not polled, falls back to the change time",
			view:        PodcastView{Meta: PodcastMeta{Title: "B"}, LastActivity: when},
			wantUpdated: true,
		},
		{
			name: "no time at all",
			view: PodcastView{Meta: PodcastMeta{Title: "C"}},
		},
		{
			name: "polled but no time recorded",
			view: PodcastView{Meta: PodcastMeta{Title: "D"}, Polled: true},
		},
	}

	tmpl := loadTemplates()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sb strings.Builder
			err := tmpl["home.html"].ExecuteTemplate(&sb, "layout.html", HomeData{Podcasts: []PodcastView{tt.view}})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			out := sb.String()

			if got := strings.Contains(out, "checked"); got != tt.wantChecked {
				t.Errorf(`"checked" present = %v, want %v`, got, tt.wantChecked)
			}
			if got := strings.Contains(out, "updated"); got != tt.wantUpdated {
				t.Errorf(`"updated" present = %v, want %v`, got, tt.wantUpdated)
			}
		})
	}
}

// A refresh and a delete arriving together must not leave the deleted slug in
// the heartbeat map: both handlers take the per-podcast lock, so the Mark
// either precedes the Forget or never happens.
func TestRefreshAndDeleteDoNotLeakHeartbeatEntry(t *testing.T) {
	app, dataDir := testApp(t)
	app.Heartbeat = NewPollHeartbeat()

	for i := range 20 {
		slug := fmt.Sprintf("racer-%d", i)
		dir := PodcastDir(dataDir, slug)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/f.xml", Title: slug}); err != nil {
			t.Fatalf("SaveMeta: %v", err)
		}

		var wg sync.WaitGroup
		wg.Go(func() {
			req := httptest.NewRequest("POST", "/podcasts/"+slug+"/refresh", nil)
			req.SetPathValue("slug", slug)
			app.handleRefreshPodcast(httptest.NewRecorder(), req)
		})
		wg.Go(func() {
			req := httptest.NewRequest("POST", "/podcasts/"+slug+"/delete", nil)
			req.SetPathValue("slug", slug)
			app.handleDeletePodcast(httptest.NewRecorder(), req)
		})
		wg.Wait()

		// The podcast is gone, so nothing may still be tracking it.
		if _, err := os.Stat(dir); err == nil {
			continue // delete lost the race; nothing to assert
		}
		if _, ok := app.Heartbeat.LastPolled(slug); ok {
			t.Fatalf("%s: deleted podcast still in the heartbeat map", slug)
		}
	}
}

// The poller marks after releasing the per-podcast lock, so a delete landing in
// that gap leaves a stale entry no Forget will ever remove. Rendering the home
// page reconciles the map against the podcasts that exist, which also stops a
// re-added podcast inheriting its predecessor's poll time.
func TestHandleHomeDropsHeartbeatEntriesForDeletedPodcasts(t *testing.T) {
	app, dataDir := testApp(t)
	app.Heartbeat = NewPollHeartbeat()

	slug := "live"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/f.xml", Title: "Live"}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	app.Heartbeat.Mark(slug, time.Now().UTC())
	// The state a poller mark racing a delete leaves behind.
	app.Heartbeat.Mark("ghost", time.Now().UTC())

	req := httptest.NewRequest("GET", "/", nil)
	app.handleHome(httptest.NewRecorder(), req)

	if _, ok := app.Heartbeat.LastPolled("ghost"); ok {
		t.Error("stale entry for a deleted podcast survived a home page render")
	}
	if _, ok := app.Heartbeat.LastPolled(slug); !ok {
		t.Error("entry for a live podcast was dropped")
	}
}

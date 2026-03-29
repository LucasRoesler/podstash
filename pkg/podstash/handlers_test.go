package podstash

import (
	"bytes"
	"errors"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

	// Verify podcast was created on disk.
	slug := Slugify("Test Podcast")
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

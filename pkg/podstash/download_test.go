package podstash

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Episode 1: The Beginning!", "episode-1-the-beginning"},
		{"What's Up? #42", "whats-up-42"},
		{"  spaces  ", "spaces"},
		{"Über Café", "uber-cafe"},
		{"", "episode"},
		{"---", "episode"},
		{"UPPERCASE", "uppercase"},
		{"a/b\\c:d", "abcd"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFileExtFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/episode.mp3", ".mp3"},
		{"https://example.com/episode.m4a", ".m4a"},
		{"https://example.com/episode.ogg", ".ogg"},
		{"https://example.com/episode.mp3?token=abc", ".mp3"},
		{"https://example.com/episode", ".mp3"}, // fallback
		{"", ".mp3"},                            // fallback
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := FileExtFromURL(tt.url)
			if got != tt.want {
				t.Errorf("FileExtFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestEpisodeFilename(t *testing.T) {
	ep := &EpisodeEntry{
		Title:        "My Great Episode!",
		PubDate:      time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
		EnclosureURL: "https://example.com/ep.mp3",
	}
	got := EpisodeFilename(ep)
	want := "2025-03-15-my-great-episode.mp3"
	if got != want {
		t.Errorf("EpisodeFilename = %q, want %q", got, want)
	}
}

func TestEpisodeFilenameNoDate(t *testing.T) {
	ep := &EpisodeEntry{
		Title:        "Undated Episode",
		EnclosureURL: "https://example.com/ep.m4a",
	}
	got := EpisodeFilename(ep)
	want := "0000-00-00-undated-episode.m4a"
	if got != want {
		t.Errorf("EpisodeFilename = %q, want %q", got, want)
	}
}

func TestDownloadEpisode(t *testing.T) {
	content := []byte("fake mp3 content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	ep := &EpisodeEntry{
		GUID:         "test-001",
		Title:        "Test Episode",
		PubDate:      time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
		EnclosureURL: srv.URL + "/episode.mp3",
	}

	err := DownloadEpisode(srv.Client(), dir, ep, nil)
	if err != nil {
		t.Fatalf("DownloadEpisode: %v", err)
	}

	wantFilename := "2025-03-15-test-episode.mp3"
	if ep.Filename != wantFilename {
		t.Fatalf("Filename = %q, want %q", ep.Filename, wantFilename)
	}
	if ep.FileSize != int64(len(content)) {
		t.Errorf("FileSize = %d, want %d", ep.FileSize, len(content))
	}
	if ep.DownloadedAt.IsZero() {
		t.Error("DownloadedAt should be set")
	}

	// Verify file exists on disk.
	path := filepath.Join(dir, ep.Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("file content mismatch")
	}

	// No temp file left behind.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !errors.Is(err, fs.ErrNotExist) {
		t.Error("temp file should not exist after download")
	}
}

func TestDownloadEpisodeSkipsExisting(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ep := &EpisodeEntry{
		GUID:         "test-002",
		Title:        "Already Downloaded",
		Filename:     "existing.mp3",
		EnclosureURL: srv.URL + "/ep.mp3",
	}

	err := DownloadEpisode(srv.Client(), dir, ep, nil)
	if err != nil {
		t.Fatalf("DownloadEpisode: %v", err)
	}
	if requestMade {
		t.Error("should not make HTTP request for already-downloaded episode")
	}
}

func TestDownloadEpisodeAdoptsExistingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make HTTP request when file exists on disk")
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ep := &EpisodeEntry{
		GUID:         "test-003",
		Title:        "Existing File",
		PubDate:      time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
		EnclosureURL: srv.URL + "/ep.mp3",
	}

	// Pre-create the file on disk.
	filename := EpisodeFilename(ep)
	os.WriteFile(filepath.Join(dir, filename), []byte("existing content"), 0644)

	err := DownloadEpisode(srv.Client(), dir, ep, nil)
	if err != nil {
		t.Fatalf("DownloadEpisode: %v", err)
	}
	if ep.Filename != filename {
		t.Errorf("Filename = %q, want %q", ep.Filename, filename)
	}
	if ep.FileSize != int64(len("existing content")) {
		t.Errorf("FileSize = %d", ep.FileSize)
	}
}

func TestDownloadCoverImage(t *testing.T) {
	content := []byte("fake image data")
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	err := DownloadCoverImage(srv.Client(), dir, srv.URL+"/cover.jpg")
	if err != nil {
		t.Fatalf("DownloadCoverImage: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("expected 1 request, got %d", requestCount)
	}

	data, err := os.ReadFile(filepath.Join(dir, coverFilename))
	if err != nil {
		t.Fatalf("cover file not found: %v", err)
	}
	if string(data) != string(content) {
		t.Error("cover content mismatch")
	}

	// Calling again should skip (file exists) without contacting the server.
	err = DownloadCoverImage(srv.Client(), dir, srv.URL+"/cover.jpg")
	if err != nil {
		t.Fatalf("second DownloadCoverImage: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected no additional requests, got %d total", requestCount)
	}
}

func TestDownloadCoverImageEmpty(t *testing.T) {
	err := DownloadCoverImage(http.DefaultClient, t.TempDir(), "")
	if err != nil {
		t.Errorf("expected no error for empty URL, got: %v", err)
	}
}

func TestDownloadPending(t *testing.T) {
	content := []byte("episode audio data")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(content)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, podcastsDir), 0755)

	// Create an active podcast with a pending episode.
	activeDir := filepath.Join(dataDir, podcastsDir, "active-pod")
	os.MkdirAll(activeDir, 0755)
	SaveMeta(activeDir, &PodcastMeta{
		FeedURL: "https://example.com/active",
		Title:   "active-pod",
	})
	SaveIndex(activeDir, &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{
				GUID:         "ep1",
				Title:        "Pending Episode",
				PubDate:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				EnclosureURL: srv.URL + "/ep.mp3",
			},
		},
	})

	// Create a paused podcast — its episodes should NOT be downloaded.
	pausedDir := filepath.Join(dataDir, podcastsDir, "paused-pod")
	os.MkdirAll(pausedDir, 0755)
	SaveMeta(pausedDir, &PodcastMeta{
		FeedURL: "https://example.com/paused",
		Title:   "paused-pod",
		Paused:  true,
	})
	SaveIndex(pausedDir, &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{
				GUID:         "ep-paused",
				Title:        "Paused Episode",
				PubDate:      time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
				EnclosureURL: srv.URL + "/paused.mp3",
			},
		},
	})

	err := DownloadPending(srv.Client(), dataDir, 2)
	if err != nil {
		t.Fatalf("DownloadPending: %v", err)
	}

	// Active podcast episode should be downloaded.
	activeIdx, err := LoadIndex(activeDir)
	if err != nil {
		t.Fatalf("LoadIndex active: %v", err)
	}
	if len(activeIdx.Episodes) != 1 {
		t.Fatalf("got %d episodes, want 1", len(activeIdx.Episodes))
	}
	if activeIdx.Episodes[0].Filename == "" {
		t.Error("active episode should have been downloaded")
	}

	// Paused podcast episode should NOT be downloaded.
	pausedIdx, err := LoadIndex(pausedDir)
	if err != nil {
		t.Fatalf("LoadIndex paused: %v", err)
	}
	if pausedIdx.Episodes[0].Filename != "" {
		t.Errorf("paused episode should not be downloaded, got Filename = %q", pausedIdx.Episodes[0].Filename)
	}
}

func TestDownloadPendingSkipsPaused(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, podcastsDir), 0755)

	// Only a paused podcast exists.
	dir := filepath.Join(dataDir, podcastsDir, "only-paused")
	os.MkdirAll(dir, 0755)
	SaveMeta(dir, &PodcastMeta{
		FeedURL: "https://example.com/paused",
		Title:   "only-paused",
		Paused:  true,
	})
	SaveIndex(dir, &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{GUID: "ep1", Title: "Ep", EnclosureURL: srv.URL + "/ep.mp3"},
		},
	})

	err := DownloadPending(srv.Client(), dataDir, 2)
	if err != nil {
		t.Fatalf("DownloadPending: %v", err)
	}
	if requestCount != 0 {
		t.Errorf("expected 0 HTTP requests for paused podcast, got %d", requestCount)
	}
}

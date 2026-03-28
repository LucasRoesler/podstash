package podstash

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseFeedSimple(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	feed, err := ParseFeed(data)
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}

	ch := feed.Channel
	if ch.Title != "Test Podcast" {
		t.Errorf("Title = %q, want %q", ch.Title, "Test Podcast")
	}
	if ch.Description != "A test podcast for unit tests." {
		t.Errorf("Description = %q", ch.Description)
	}
	if ch.Language != "en" {
		t.Errorf("Language = %q, want %q", ch.Language, "en")
	}
	if ch.ImageURL() != "https://example.com/cover.jpg" {
		t.Errorf("ImageURL = %q", ch.ImageURL())
	}
	if len(ch.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(ch.Items))
	}

	ep := ch.Items[0]
	if ep.Title != "Episode 2: The Second" {
		t.Errorf("Items[0].Title = %q", ep.Title)
	}
	if ep.GUID != "ep-guid-002" {
		t.Errorf("Items[0].GUID = %q", ep.GUID)
	}
	if ep.Enclosure.URL != "https://example.com/ep2.mp3" {
		t.Errorf("Items[0].Enclosure.URL = %q", ep.Enclosure.URL)
	}
	if ep.Enclosure.Type != "audio/mpeg" {
		t.Errorf("Items[0].Enclosure.Type = %q", ep.Enclosure.Type)
	}
}

func TestParseFeedXML11(t *testing.T) {
	data := []byte(`<?xml version="1.1" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>XML 1.1 Feed</title>
    <item>
      <title>Episode</title>
      <guid>ep-1</guid>
      <enclosure url="https://example.com/ep.mp3" type="audio/mpeg"/>
    </item>
  </channel>
</rss>`)

	feed, err := ParseFeed(data)
	if err != nil {
		t.Fatalf("ParseFeed with XML 1.1: %v", err)
	}
	if feed.Channel.Title != "XML 1.1 Feed" {
		t.Errorf("Title = %q", feed.Channel.Title)
	}
}

func TestParseFeedITunes(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_itunes.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	feed, err := ParseFeed(data)
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}

	ch := feed.Channel
	if ch.Author() != "Podcast Author" {
		t.Errorf("Author = %q, want %q", ch.Author(), "Podcast Author")
	}
	if ch.ITunesImage != "https://example.com/itunes-cover.jpg" {
		t.Errorf("ITunesImage = %q, want itunes image", ch.ITunesImage)
	}
	if ch.ImageURL() != "https://example.com/itunes-cover.jpg" {
		t.Errorf("ImageURL() = %q, want itunes image preferred", ch.ImageURL())
	}
	if ch.Language != "de" {
		t.Errorf("Language = %q, want %q", ch.Language, "de")
	}

	if len(ch.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(ch.Items))
	}

	ep := ch.Items[0]
	if ep.ITunesDuration != "01:23:45" {
		t.Errorf("ITunesDuration = %q", ep.ITunesDuration)
	}
	if ep.ITunesImage.Href != "https://example.com/ep-cover.jpg" {
		t.Errorf("ITunesImage = %q", ep.ITunesImage.Href)
	}

	// Second episode has m4a enclosure.
	ep2 := ch.Items[1]
	if ep2.Enclosure.Type != "audio/x-m4a" {
		t.Errorf("Items[1].Enclosure.Type = %q", ep2.Enclosure.Type)
	}
}

func TestParsePubDate(t *testing.T) {
	want := time.Date(2025, 3, 3, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input string
		want  time.Time
	}{
		{"RFC1123Z", "Mon, 03 Mar 2025 14:30:00 +0000", want},
		{"RFC1123", "Mon, 03 Mar 2025 14:30:00 GMT", want},
		{"RFC3339", "2025-03-03T14:30:00Z", want},
		{"single digit day", "Mon, 3 Mar 2025 14:30:00 +0000", want},
		{"empty", "", time.Time{}},
		{"garbage", "not a date", time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePubDate(tt.input)
			if !got.Equal(tt.want) {
				t.Errorf("ParsePubDate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseFeedDates(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_dates.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	feed, err := ParseFeed(data)
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}

	want := time.Date(2025, 3, 3, 14, 30, 0, 0, time.UTC)

	for i, item := range feed.Channel.Items {
		got := ParsePubDate(item.PubDate)
		if i < 4 {
			// First 4 items should all parse to the same time.
			if !got.Equal(want) {
				t.Errorf("Items[%d] %q: parsed to %v, want %v", i, item.Title, got, want)
			}
		} else {
			// "No Date" item should parse to zero time.
			if !got.IsZero() {
				t.Errorf("Items[%d] %q: expected zero time, got %v", i, item.Title, got)
			}
		}
	}
}

func TestFetchFeed(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(data)
	}))
	defer srv.Close()

	feed, err := FetchFeed(srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchFeed: %v", err)
	}
	if feed.Channel.Title != "Test Podcast" {
		t.Errorf("Title = %q", feed.Channel.Title)
	}
}

func TestFetchFeedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchFeed(srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestRefreshPodcast(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	// Set up a podcast directory with meta but no index.
	dataDir := t.TempDir()
	slug := "test-podcast"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)

	meta := &PodcastMeta{
		FeedURL: srv.URL,
		Title:   "Test Podcast",
		AddedAt: time.Now().UTC(),
	}
	if err := SaveMeta(dir, meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	// First refresh should add 2 episodes.
	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("RefreshPodcast: %v", err)
	}
	if added != 2 {
		t.Errorf("first refresh: added %d, want 2", added)
	}

	// Second refresh with same feed should add 0.
	added, err = RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("RefreshPodcast (second): %v", err)
	}
	if added != 0 {
		t.Errorf("second refresh: added %d, want 0", added)
	}

	// Verify index has 2 episodes.
	idx, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.Episodes) != 2 {
		t.Errorf("index has %d episodes, want 2", len(idx.Episodes))
	}

	// Verify meta was updated.
	updatedMeta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if updatedMeta.LastCheckedAt.IsZero() {
		t.Error("LastCheckedAt should be set after refresh")
	}

	// Verify metadata from feed was applied.
	if updatedMeta.ImageURL != "https://example.com/cover.jpg" {
		t.Errorf("ImageURL = %q", updatedMeta.ImageURL)
	}
}

func TestRefreshPodcastDeduplication(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "dedup-test"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)

	meta := &PodcastMeta{FeedURL: srv.URL, Title: "Dedup Test"}
	SaveMeta(dir, meta)

	// Pre-populate index with one of the GUIDs.
	idx := &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{GUID: "ep-guid-001", Title: "Already Known"},
		},
	}
	SaveIndex(dir, idx)

	// Refresh should only add the one new episode.
	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("RefreshPodcast: %v", err)
	}
	if added != 1 {
		t.Errorf("added %d, want 1 (one already existed)", added)
	}

	reloaded, _ := LoadIndex(dir)
	if len(reloaded.Episodes) != 2 {
		t.Errorf("index has %d episodes, want 2", len(reloaded.Episodes))
	}

	// Verify the pre-existing episode is still first.
	if reloaded.Episodes[0].Title != "Already Known" {
		t.Errorf("first episode title = %q, want %q", reloaded.Episodes[0].Title, "Already Known")
	}
}

func TestRefreshPodcastUpdatesFromFeed(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_itunes.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "itunes-test"
	dir := filepath.Join(dataDir, podcastsDir, slug)
	os.MkdirAll(dir, 0755)

	meta := &PodcastMeta{FeedURL: srv.URL, Title: "Old Title"}
	SaveMeta(dir, meta)

	RefreshPodcast(srv.Client(), dataDir, slug)

	updated, _ := LoadMeta(dir)
	if updated.Title != "iTunes Podcast" {
		t.Errorf("Title = %q, want %q", updated.Title, "iTunes Podcast")
	}
	if updated.Author != "Podcast Author" {
		t.Errorf("Author = %q, want %q", updated.Author, "Podcast Author")
	}
	if updated.ImageURL != "https://example.com/itunes-cover.jpg" {
		t.Errorf("ImageURL = %q", updated.ImageURL)
	}
}

func TestRefreshPodcastSkipPatterns(t *testing.T) {
	// Feed with 2 episodes: "Episode 2: The Second" and "Episode 1: The Beginning"
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "skip-test"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)

	// Set up a skip pattern that matches "The Second" in the title.
	meta := &PodcastMeta{
		FeedURL:      srv.URL,
		Title:        "Skip Test",
		SkipPatterns: []string{`(?i)the\s+second`},
	}
	SaveMeta(dir, meta)

	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("RefreshPodcast: %v", err)
	}
	if added != 2 {
		t.Errorf("added %d, want 2 (both added to index)", added)
	}

	idx, _ := LoadIndex(dir)
	if len(idx.Episodes) != 2 {
		t.Fatalf("got %d episodes, want 2", len(idx.Episodes))
	}

	// Find the skipped episode.
	var skippedCount, unskippedCount int
	for _, ep := range idx.Episodes {
		if ep.Skipped {
			skippedCount++
			if ep.Title != "Episode 2: The Second" {
				t.Errorf("expected 'Episode 2: The Second' to be skipped, got %q", ep.Title)
			}
		} else {
			unskippedCount++
		}
	}
	if skippedCount != 1 {
		t.Errorf("skipped %d episodes, want 1", skippedCount)
	}
	if unskippedCount != 1 {
		t.Errorf("unskipped %d episodes, want 1", unskippedCount)
	}
}

func TestRefreshPodcastDownloadAfter(t *testing.T) {
	// Feed has 2 episodes:
	//   "Episode 2: The Second" - pubDate Sat, 15 Mar 2025
	//   "Episode 1: The Beginning" - pubDate Sat, 08 Mar 2025
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "date-filter-test"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)

	// Only download episodes after March 10, 2025. Episode 1 (March 8) should be skipped.
	cutoff := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)
	meta := &PodcastMeta{
		FeedURL:       srv.URL,
		Title:         "Date Filter Test",
		DownloadAfter: &cutoff,
	}
	SaveMeta(dir, meta)

	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("RefreshPodcast: %v", err)
	}
	if added != 2 {
		t.Errorf("added %d, want 2 (both added to index)", added)
	}

	idx, _ := LoadIndex(dir)
	var skipped, pending int
	for _, ep := range idx.Episodes {
		if ep.Skipped {
			skipped++
			if ep.Title != "Episode 1: The Beginning" {
				t.Errorf("expected 'Episode 1: The Beginning' to be skipped, got %q", ep.Title)
			}
		} else {
			pending++
		}
	}
	if skipped != 1 {
		t.Errorf("skipped %d, want 1", skipped)
	}
	if pending != 1 {
		t.Errorf("pending %d, want 1", pending)
	}
}

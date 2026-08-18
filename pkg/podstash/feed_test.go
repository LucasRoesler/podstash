package podstash

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	if _, err = RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("RefreshPodcast: %v", err)
	}

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

func TestGenerateFeed(t *testing.T) {
	meta := &PodcastMeta{
		Slug:        "my-podcast",
		Title:       "My Podcast",
		Description: "A test podcast",
	}

	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	episodes := []EpisodeEntry{
		{GUID: "ep1", Title: "First", PubDate: older, Filename: "2025-01-01-first.mp3", FileSize: 1000, EnclosureType: "audio/mpeg"},
		{GUID: "ep2", Title: "Second", PubDate: newer, Filename: "2025-03-01-second.mp3", FileSize: 2000, EnclosureType: "audio/mpeg"},
		{GUID: "ep3", Title: "Not Downloaded", PubDate: newer}, // no Filename — must be excluded
	}

	data, err := GenerateFeed(meta, episodes, "http://localhost:8080")
	if err != nil {
		t.Fatalf("GenerateFeed: %v", err)
	}

	xml := string(data)

	// Only downloaded episodes should appear.
	if !strings.Contains(xml, "First") {
		t.Error("feed should contain downloaded episode 'First'")
	}
	if !strings.Contains(xml, "Second") {
		t.Error("feed should contain downloaded episode 'Second'")
	}
	if strings.Contains(xml, "Not Downloaded") {
		t.Error("feed should NOT contain episode without a local file")
	}

	// Enclosure URLs should point to the local server.
	if !strings.Contains(xml, "http://localhost:8080/podcasts/my-podcast/episodes/2025-01-01-first.mp3") {
		t.Error("feed should contain local enclosure URL for first episode")
	}
	if !strings.Contains(xml, "http://localhost:8080/podcasts/my-podcast/episodes/2025-03-01-second.mp3") {
		t.Error("feed should contain local enclosure URL for second episode")
	}

	// Newer episode should appear before older (newest-first ordering).
	firstIdx := strings.Index(xml, "2025-03-01-second.mp3")
	secondIdx := strings.Index(xml, "2025-01-01-first.mp3")
	if firstIdx > secondIdx {
		t.Error("episodes should be sorted newest-first")
	}

	// Cover image URL should appear in both <image> and <itunes:image>.
	coverURL := "http://localhost:8080/podcasts/my-podcast/cover.jpg"
	if !strings.Contains(xml, coverURL) {
		t.Errorf("feed should contain cover URL %q", coverURL)
	}
	if !strings.Contains(xml, `itunes:image`) {
		t.Error("feed should contain itunes:image element")
	}
}

func TestGenerateFeedEmpty(t *testing.T) {
	meta := &PodcastMeta{Slug: "empty", Title: "Empty Podcast"}

	data, err := GenerateFeed(meta, nil, "http://localhost:8080")
	if err != nil {
		t.Fatalf("GenerateFeed: %v", err)
	}

	xml := string(data)
	if !strings.Contains(xml, "Empty Podcast") {
		t.Error("feed should contain podcast title")
	}
	if strings.Contains(xml, "<item>") {
		t.Error("feed should have no items when no episodes are downloaded")
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

// Regression test for issue #8: refreshing an unchanged feed rewrote the full
// index every poll (3.7 MB per poll for a 6-feed library), which on a spinning
// disk prevented spindown. A refresh that adds nothing must leave the index
// file untouched.
func TestRefreshPodcastUnchangedFeedLeavesIndexUntouched(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "unchanged-feed"
	dir := PodcastDir(dataDir, slug)
	os.MkdirAll(dir, 0755)

	meta := &PodcastMeta{FeedURL: srv.URL, Title: "Unchanged Feed", AddedAt: time.Now().UTC()}
	if err := SaveMeta(dir, meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	indexPath := filepath.Join(dir, indexFilename)
	before := fileIdentity(t, indexPath)

	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if added != 0 {
		t.Fatalf("added = %d, want 0", added)
	}

	if after := fileIdentity(t, indexPath); after != before {
		t.Errorf("index rewritten on unchanged refresh: inode %d -> %d", before, after)
	}
}

func TestFetchFeedConditionalSendsValidators(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"abc123"`
	const lastMod = "Wed, 21 Oct 2026 07:28:00 GMT"

	var gotINM, gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastMod)
		if gotINM == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()

	// First fetch: no validators to send, full body returned.
	feed, validators, err := FetchFeedConditional(srv.Client(), srv.URL, FeedValidators{})
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if feed == nil {
		t.Fatal("first fetch returned no feed")
	}
	if gotINM != "" || gotIMS != "" {
		t.Errorf("first fetch sent validators: If-None-Match=%q If-Modified-Since=%q", gotINM, gotIMS)
	}
	if validators.ETag != etag {
		t.Errorf("ETag = %q, want %q", validators.ETag, etag)
	}
	if validators.LastModified != lastMod {
		t.Errorf("Last-Modified = %q, want %q", validators.LastModified, lastMod)
	}

	// Second fetch: validators echoed back, server answers 304.
	feed, next, err := FetchFeedConditional(srv.Client(), srv.URL, validators)
	if !errors.Is(err, ErrFeedNotModified) {
		t.Fatalf("second fetch err = %v, want ErrFeedNotModified", err)
	}
	if feed != nil {
		t.Error("304 returned a parsed feed, want nil")
	}
	if gotINM != etag {
		t.Errorf("If-None-Match = %q, want %q", gotINM, etag)
	}
	if gotIMS != lastMod {
		t.Errorf("If-Modified-Since = %q, want %q", gotIMS, lastMod)
	}
	if next.ETag != etag {
		t.Errorf("validators not carried through 304: ETag = %q, want %q", next.ETag, etag)
	}
}

// A 304 response need not repeat the validators, so the ones that produced the
// hit must be carried forward or the next poll would fetch unconditionally.
func TestFetchFeedConditionalKeepsValidatorsOnBare304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	prev := FeedValidators{ETag: `"kept"`, LastModified: "Wed, 21 Oct 2026 07:28:00 GMT"}
	_, next, err := FetchFeedConditional(srv.Client(), srv.URL, prev)
	if !errors.Is(err, ErrFeedNotModified) {
		t.Fatalf("err = %v, want ErrFeedNotModified", err)
	}
	if next.ETag != prev.ETag {
		t.Errorf("ETag = %q, want %q", next.ETag, prev.ETag)
	}
	if next.LastModified != prev.LastModified {
		t.Errorf("Last-Modified = %q, want %q", next.LastModified, prev.LastModified)
	}
}

func TestRefreshPodcastStoresAndReusesValidators(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"feed-v1"`
	var requests, fullBodies int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fullBodies++
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "conditional-feed"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: srv.URL, Title: "Conditional", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.ETag != etag {
		t.Fatalf("stored ETag = %q, want %q", meta.ETag, etag)
	}

	// Second refresh must reuse the stored ETag and take the 304 path.
	added, err = RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	if fullBodies != 1 {
		t.Errorf("full feed bodies sent = %d, want 1 (second poll should be a 304)", fullBodies)
	}

	// The 304 path must still persist meta. Comparing against the value the
	//200 path stored would pass even if the 304 branch saved nothing, so
	// LastCheckedAt is forced backwards on disk first: only a save performed
	// by the 304 branch itself can move it forward again.
	meta, err = LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	// Poison LastCheckedAt only: it does not affect request routing, so the
	// next refresh still takes the 304 path, and only a save performed by that
	// branch can move it forward again. Poisoning the ETag instead would send
	// a validator the server rejects, turning the very poll under test into a
	// 200 and testing nothing.
	stale := time.Now().UTC().Add(-24 * time.Hour)
	meta.LastCheckedAt = stale
	if err := SaveMeta(dir, meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("third refresh: %v", err)
	}

	meta, err = LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if !meta.LastCheckedAt.After(stale) {
		t.Errorf("LastCheckedAt = %v, want advanced past %v by the 304 path", meta.LastCheckedAt, stale)
	}
	if meta.ETag != etag {
		t.Errorf("ETag after 304 = %q, want %q", meta.ETag, etag)
	}
}

// A 304 must not disturb episodes already recorded in the index.
func TestRefreshPodcastNotModifiedPreservesIndex(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"stable"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "preserve-index"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: srv.URL, Title: "Preserve", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	before, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	after, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(after.Episodes) != len(before.Episodes) {
		t.Fatalf("episodes = %d, want %d unchanged across a 304", len(after.Episodes), len(before.Episodes))
	}
	// Compare full entries, not just the count: a 304 must leave GUIDs,
	// filenames and download records exactly as they were.
	if !reflect.DeepEqual(after.Episodes, before.Episodes) {
		t.Errorf("episode entries changed across a 304:\nbefore: %+v\nafter:  %+v",
			before.Episodes, after.Episodes)
	}
}

// A server that stops sending validators must not leave the old ones stored:
// echoing a validator the server no longer recognises would be meaningless at
// best and could suppress a real update at worst. Only the 304 branch carries
// validators forward; a 200 always takes them fresh from the response.
func TestFetchFeedConditionalClearsValidatorsWhenServerStopsSendingThem(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	sendETag := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sendETag {
			w.Header().Set("ETag", `"v1"`)
		}
		w.Write(data)
	}))
	defer srv.Close()

	_, first, err := FetchFeedConditional(srv.Client(), srv.URL, FeedValidators{})
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if first.ETag != `"v1"` {
		t.Fatalf("ETag = %q, want %q", first.ETag, `"v1"`)
	}

	sendETag = false
	_, second, err := FetchFeedConditional(srv.Client(), srv.URL, first)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if second.ETag != "" {
		t.Errorf("stale ETag retained on 200: %q, want cleared", second.ETag)
	}
}

// Validators are persisted and resent every poll, so an implausibly long one
// from a hostile or broken server is dropped rather than stored.
func TestFetchFeedConditionalDropsOversizedValidators(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	atLimit := strings.Repeat("a", maxValidatorLen)
	overLimit := strings.Repeat("a", maxValidatorLen+1)

	var sendETagRef, sendLMRef *string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", *sendETagRef)
		w.Header().Set("Last-Modified", *sendLMRef)
		w.Write(data)
	}))
	defer srv.Close()

	// Drive the real fetch path: calling boundedValidator directly here would
	// still pass if the production code stopped calling it.
	var sendETag, sendLM string
	sendETagRef, sendLMRef = &sendETag, &sendLM
	fetch := func(t *testing.T, etag, lastMod string) FeedValidators {
		t.Helper()
		sendETag, sendLM = etag, lastMod
		_, validators, err := FetchFeedConditional(srv.Client(), srv.URL, FeedValidators{})
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		return validators
	}

	// Exactly at the limit is kept; one byte over is dropped. Pins the boundary
	// so a > / >= slip is caught.
	if got := fetch(t, atLimit, atLimit); got.ETag != atLimit {
		t.Errorf("ETag at the limit (%d bytes) was dropped", maxValidatorLen)
	} else if got.LastModified != atLimit {
		t.Errorf("Last-Modified at the limit (%d bytes) was dropped", maxValidatorLen)
	}

	got := fetch(t, overLimit, overLimit)
	if got.ETag != "" {
		t.Errorf("oversized ETag stored (%d bytes), want dropped", len(got.ETag))
	}
	if got.LastModified != "" {
		t.Errorf("oversized Last-Modified stored (%d bytes), want dropped", len(got.LastModified))
	}
}

// Regression test: a 304 says the feed is unchanged relative to what we stored,
// which is worthless if the stored index is gone. Taking the fast path anyway
// would strand the podcast with an empty index forever, since the same ETag
// returns 304 on every future poll.
func TestRefreshPodcastRebuildsMissingIndexDespiteValidators(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"v1"`
	conditionalRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			conditionalRequests++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "recover"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: srv.URL, Title: "Recover", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	before, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(before.Episodes) == 0 {
		t.Fatal("first refresh indexed nothing")
	}

	if err := os.Remove(filepath.Join(dir, indexFilename)); err != nil {
		t.Fatalf("remove index: %v", err)
	}

	added, err := RefreshPodcast(srv.Client(), dataDir, slug)
	if err != nil {
		t.Fatalf("recovery refresh: %v", err)
	}
	if added != len(before.Episodes) {
		t.Errorf("added = %d, want %d re-added from a full fetch", added, len(before.Episodes))
	}
	if conditionalRequests != 0 {
		t.Errorf("sent %d conditional requests with no index on disk, want 0", conditionalRequests)
	}

	after, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex after recovery: %v", err)
	}
	if len(after.Episodes) != len(before.Episodes) {
		t.Errorf("episodes = %d, want %d rebuilt", len(after.Episodes), len(before.Episodes))
	}
}

// A corrupt index must keep erroring rather than being silently discarded: it
// holds download records, so podstash must not decide on its own to throw it
// away and re-fetch everything. Established with a stored ETag and a populated
// index first, so corrupting the index is the only thing that changes, and a
// version that skipped the parse would silently take the 304 path instead.
func TestRefreshPodcastReportsCorruptIndex(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"v1"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "corrupt"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: srv.URL, Title: "Corrupt", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	// Populate the index and store the ETag, so the conditional path is live.
	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.ETag != etag {
		t.Fatalf("setup did not store the ETag: %q", meta.ETag)
	}

	if err := os.WriteFile(filepath.Join(dir, indexFilename), []byte("{ not json"), 0644); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err == nil {
		t.Error("refresh with a corrupt index returned nil, want an error")
	}
}

// A refresh a person asked for must re-fetch the feed rather than silently
// taking the 304 fast path, which would make the refresh button look broken.
func TestForceRefreshPodcastIgnoresValidators(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"v1"`
	var conditional, full int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full++
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "forced"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: srv.URL, Title: "Forced", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if full != 1 {
		t.Fatalf("full fetches after first refresh = %d, want 1", full)
	}

	// A background poll takes the 304 path.
	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("polled refresh: %v", err)
	}
	if conditional != 1 {
		t.Errorf("conditional requests = %d, want 1", conditional)
	}
	if full != 1 {
		t.Errorf("full fetches = %d, want still 1 after a polled refresh", full)
	}

	// A forced refresh must fetch in full despite the stored validators.
	if _, err := ForceRefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	if full != 2 {
		t.Errorf("full fetches = %d, want 2 after a forced refresh", full)
	}
	if conditional != 1 {
		t.Errorf("forced refresh sent a conditional request: %d, want 1", conditional)
	}
}

// The 304 branch must persist the validators it carried forward, not merely
// leave whatever the last 200 wrote. A server that omits the ETag on its 304
// makes the difference observable: only the carry-forward keeps it on disk,
// so a branch that saves nothing (or saves the empty response value) is caught.
func TestRefreshPodcastPersistsValidatorsCarriedThrough304(t *testing.T) {
	data, err := os.ReadFile("testdata/feed_simple.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const etag = `"v1"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			// Deliberately no ETag header on the 304, which RFC 9110 permits.
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Write(data)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	slug := "carried"
	dir := PodcastDir(dataDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: srv.URL, Title: "Carried", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Blank the stored ETag's companion field so the only way it can be on disk
	// after the next poll is the 304 branch writing it back.
	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	meta.LastModified = "Wed, 21 Oct 2026 07:28:00 GMT"
	if err := SaveMeta(dir, meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := RefreshPodcast(srv.Client(), dataDir, slug); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	meta, err = LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.ETag != etag {
		t.Errorf("ETag = %q, want %q carried through the 304", meta.ETag, etag)
	}
}

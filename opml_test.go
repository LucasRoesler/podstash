package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseOPML(t *testing.T) {
	f, err := os.Open("testdata/import.opml")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	urls, err := ParseOPML(f)
	if err != nil {
		t.Fatalf("ParseOPML: %v", err)
	}

	want := []string{
		"https://example.com/the-daily/feed.xml",
		"https://example.com/hardcore-history/feed.xml",
		"https://example.com/top-level/feed.xml",
	}

	if len(urls) != len(want) {
		t.Fatalf("got %d URLs, want %d", len(urls), len(want))
	}
	for i, u := range urls {
		if u != want[i] {
			t.Errorf("urls[%d] = %q, want %q", i, u, want[i])
		}
	}
}

func TestParseOPMLNested(t *testing.T) {
	opml := `<?xml version="1.0"?>
<opml version="2.0">
  <body>
    <outline text="Group A">
      <outline text="Sub Group">
        <outline type="rss" text="Deep" xmlUrl="https://example.com/deep"/>
      </outline>
    </outline>
    <outline type="rss" text="Flat" xmlUrl="https://example.com/flat"/>
  </body>
</opml>`

	urls, err := ParseOPML(strings.NewReader(opml))
	if err != nil {
		t.Fatalf("ParseOPML: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("got %d URLs, want 2", len(urls))
	}
	if urls[0] != "https://example.com/deep" {
		t.Errorf("urls[0] = %q", urls[0])
	}
	if urls[1] != "https://example.com/flat" {
		t.Errorf("urls[1] = %q", urls[1])
	}
}

func TestExportOPML(t *testing.T) {
	podcasts := []PodcastMeta{
		{Title: "Podcast A", FeedURL: "https://example.com/a"},
		{Title: "Podcast B", FeedURL: "https://example.com/b"},
	}

	data, err := ExportOPML(podcasts)
	if err != nil {
		t.Fatalf("ExportOPML: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Error("missing XML header")
	}
	if !strings.Contains(s, `xmlUrl="https://example.com/a"`) {
		t.Error("missing feed URL A")
	}
	if !strings.Contains(s, `xmlUrl="https://example.com/b"`) {
		t.Error("missing feed URL B")
	}
	if !strings.Contains(s, `text="Podcast A"`) {
		t.Error("missing podcast title A")
	}

	// Verify it round-trips.
	urls, err := ParseOPML(strings.NewReader(s))
	if err != nil {
		t.Fatalf("round-trip ParseOPML: %v", err)
	}
	if len(urls) != 2 {
		t.Errorf("round-trip got %d URLs, want 2", len(urls))
	}
}

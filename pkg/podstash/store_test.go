package podstash

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"The Daily", "the-daily"},
		{"Dan Carlin's Hardcore History", "dan-carlins-hardcore-history"},
		{"WTF with Marc Maron!", "wtf-with-marc-maron"},
		{"  Spaces Everywhere  ", "spaces-everywhere"},
		{"UPPER CASE", "upper-case"},
		{"already-kebab", "already-kebab"},
		{"Episode #42: Special!", "episode-42-special"},
		{"Über Podcast — Great Stuff", "uber-podcast-great-stuff"},
		{"tagesschau in Einfacher Sprache", "tagesschau-in-einfacher-sprache"},
		{"", "podcast"},
		{"---", "podcast"},
		{"A Really Long Title That Exceeds The Maximum Length Allowed For A Slug Because It Just Goes On And On Forever", "a-really-long-title-that-exceeds-the-maximum-length-allowed-for-a-slug-because-i"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Slugify(tt.input)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidSlug(t *testing.T) {
	tests := []struct {
		slug string
		want bool
	}{
		{"the-daily", true},
		{"podcast-123", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../etc", false},
		{"foo/bar", false},
		{"foo\\bar", false},
	}
	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			if got := ValidSlug(tt.slug); got != tt.want {
				t.Errorf("ValidSlug(%q) = %v, want %v", tt.slug, got, tt.want)
			}
		})
	}
}

func TestSaveAndLoadMeta(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	original := &PodcastMeta{
		FeedURL:       "https://example.com/feed.xml",
		Title:         "Test Podcast",
		Author:        "Test Author",
		Description:   "A test podcast.",
		ImageURL:      "https://example.com/image.jpg",
		AddedAt:       now,
		LastChangedAt: now,
		Paused:        false,
	}

	if err := SaveMeta(dir, original); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	loaded, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}

	// Slug should be derived from directory name.
	if loaded.Slug != filepath.Base(dir) {
		t.Errorf("Slug = %q, want %q", loaded.Slug, filepath.Base(dir))
	}

	if loaded.FeedURL != original.FeedURL {
		t.Errorf("FeedURL = %q, want %q", loaded.FeedURL, original.FeedURL)
	}
	if loaded.Title != original.Title {
		t.Errorf("Title = %q, want %q", loaded.Title, original.Title)
	}
	if loaded.Author != original.Author {
		t.Errorf("Author = %q, want %q", loaded.Author, original.Author)
	}
	if loaded.Paused != original.Paused {
		t.Errorf("Paused = %v, want %v", loaded.Paused, original.Paused)
	}
}

func TestSaveAndLoadIndex(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	original := &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{
				GUID:          "ep-001",
				Title:         "First Episode",
				PubDate:       now.Add(-24 * time.Hour),
				EnclosureURL:  "https://example.com/ep1.mp3",
				EnclosureType: "audio/mpeg",
				Description:   "The first episode.",
				Filename:      "2024-03-15-first-episode.mp3",
				DownloadedAt:  now,
				FileSize:      12345678,
			},
			{
				GUID:          "ep-002",
				Title:         "Second Episode",
				PubDate:       now,
				EnclosureURL:  "https://example.com/ep2.mp3",
				EnclosureType: "audio/mpeg",
				Description:   "The second episode.",
				// Not downloaded yet — Filename empty.
			},
		},
	}

	if err := SaveIndex(dir, original); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}

	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	if len(loaded.Episodes) != 2 {
		t.Fatalf("got %d episodes, want 2", len(loaded.Episodes))
	}
	if loaded.Episodes[0].GUID != "ep-001" {
		t.Errorf("Episodes[0].GUID = %q, want %q", loaded.Episodes[0].GUID, "ep-001")
	}
	if loaded.Episodes[1].Filename != "" {
		t.Errorf("Episodes[1].Filename = %q, want empty", loaded.Episodes[1].Filename)
	}
	if loaded.Episodes[0].FileSize != 12345678 {
		t.Errorf("Episodes[0].FileSize = %d, want 12345678", loaded.Episodes[0].FileSize)
	}
}

func TestLoadIndexMissing(t *testing.T) {
	dir := t.TempDir()
	idx, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex on missing file: %v", err)
	}
	if len(idx.Episodes) != 0 {
		t.Errorf("expected empty index, got %d episodes", len(idx.Episodes))
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	meta := &PodcastMeta{
		FeedURL: "https://example.com/feed.xml",
		Title:   "Test",
	}

	if err := SaveMeta(dir, meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	// Verify no temp file left behind.
	tmpPath := filepath.Join(dir, metaFilename+".tmp")
	if _, err := os.Stat(tmpPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temp file should not exist after successful write")
	}

	// Verify the final file exists.
	finalPath := filepath.Join(dir, metaFilename)
	if _, err := os.Stat(finalPath); err != nil {
		t.Errorf("final file should exist: %v", err)
	}
}

func TestHasGUID(t *testing.T) {
	idx := &EpisodeIndex{
		Episodes: []EpisodeEntry{
			{GUID: "aaa"},
			{GUID: "bbb"},
		},
	}

	if !idx.HasGUID("aaa") {
		t.Error("HasGUID(aaa) = false, want true")
	}
	if idx.HasGUID("ccc") {
		t.Error("HasGUID(ccc) = true, want false")
	}
}

func TestListPodcasts(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, podcastsDir)
	os.MkdirAll(root, 0755)

	// Create two podcast directories with meta files.
	for _, name := range []string{"podcast-a", "podcast-b"} {
		dir := filepath.Join(root, name)
		os.MkdirAll(dir, 0755)
		meta := &PodcastMeta{
			FeedURL: "https://example.com/" + name,
			Title:   name,
		}
		if err := SaveMeta(dir, meta); err != nil {
			t.Fatalf("SaveMeta(%s): %v", name, err)
		}
	}

	// Create a directory without meta (should be skipped).
	os.MkdirAll(filepath.Join(root, "no-meta"), 0755)

	podcasts, err := ListPodcasts(dataDir)
	if err != nil {
		t.Fatalf("ListPodcasts: %v", err)
	}
	if len(podcasts) != 2 {
		t.Errorf("got %d podcasts, want 2", len(podcasts))
	}

	// Verify slugs are set.
	for _, p := range podcasts {
		if p.Slug == "" {
			t.Errorf("podcast %q has empty slug", p.Title)
		}
	}
}

func TestCompileSkipPatterns(t *testing.T) {
	patterns := []string{`(?i)best\s+of`, `rebroadcast`, `[invalid`}
	compiled := CompileSkipPatterns(patterns)
	// Should compile 2 valid patterns, skip the invalid one.
	if len(compiled) != 2 {
		t.Fatalf("got %d compiled patterns, want 2", len(compiled))
	}
}

func TestMatchesSkipPattern(t *testing.T) {
	patterns := CompileSkipPatterns([]string{`(?i)best\s+of`, `(?i)rebroadcast`})

	tests := []struct {
		title string
		desc  string
		want  bool
	}{
		{"Regular Episode", "A normal description.", false},
		{"Best Of 2024", "Highlights from the year.", true},
		{"BEST OF December", "Monthly recap.", true},
		{"Episode 10", "This is a rebroadcast of episode 5.", true},
		{"Rebroadcast: Classic Episode", "Original air date 2020.", true},
		{"Episode 42", "No matching content here.", false},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := MatchesSkipPattern(patterns, tt.title, tt.desc)
			if got != tt.want {
				t.Errorf("MatchesSkipPattern(%q, %q) = %v, want %v", tt.title, tt.desc, got, tt.want)
			}
		})
	}
}

func TestMatchesSkipPatternEmpty(t *testing.T) {
	// No patterns should never match.
	if MatchesSkipPattern(nil, "anything", "at all") {
		t.Error("nil patterns should never match")
	}
}

func TestSkipPatternsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	meta := &PodcastMeta{
		FeedURL:      "https://example.com/feed.xml",
		Title:        "Test",
		SkipPatterns: []string{`(?i)best\s+of`, `(?i)rebroadcast`},
	}
	if err := SaveMeta(dir, meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	loaded, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if len(loaded.SkipPatterns) != 2 {
		t.Fatalf("got %d skip patterns, want 2", len(loaded.SkipPatterns))
	}
	if loaded.SkipPatterns[0] != `(?i)best\s+of` {
		t.Errorf("SkipPatterns[0] = %q", loaded.SkipPatterns[0])
	}
}

func TestListPodcastsMissingDir(t *testing.T) {
	dataDir := t.TempDir()
	// Don't create the podcasts/ subdirectory.
	podcasts, err := ListPodcasts(dataDir)
	if err != nil {
		t.Fatalf("ListPodcasts on missing dir: %v", err)
	}
	if len(podcasts) != 0 {
		t.Errorf("expected 0 podcasts, got %d", len(podcasts))
	}
}

// Regression test for issue #8: rewriting an unchanged index every poll kept
// spinning disks awake. An identical payload must leave the file untouched.
// fileIdentity returns the inode of path. atomicWriteJSON writes a temp file and
// renames it over the target, so a write always changes the inode. Comparing
// identity detects a write exactly, where comparing mtime depends on the
// filesystem's timestamp granularity.
func fileIdentity(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("inode unavailable on this platform for %s", path)
	}
	return st.Ino
}

func TestAtomicWriteJSONSkipsIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	idx := &EpisodeIndex{Episodes: []EpisodeEntry{{GUID: "a", Title: "Episode A"}}}

	if err := SaveIndex(dir, idx); err != nil {
		t.Fatalf("first save: %v", err)
	}

	path := filepath.Join(dir, indexFilename)
	before := fileIdentity(t, path)

	if err := SaveIndex(dir, idx); err != nil {
		t.Fatalf("second save: %v", err)
	}

	if after := fileIdentity(t, path); after != before {
		t.Errorf("index rewritten on identical save: inode %d -> %d", before, after)
	}
}

func TestAtomicWriteJSONWritesChangedContent(t *testing.T) {
	dir := t.TempDir()

	if err := SaveIndex(dir, &EpisodeIndex{Episodes: []EpisodeEntry{{GUID: "a"}}}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := SaveIndex(dir, &EpisodeIndex{Episodes: []EpisodeEntry{{GUID: "a"}, {GUID: "b"}}}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	if len(got.Episodes) != 2 {
		t.Errorf("episodes = %d, want 2", len(got.Episodes))
	}
}

func TestCheckDataDirWritable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, podcastsDir), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := CheckDataDirWritable(dir); err != nil {
		t.Errorf("CheckDataDirWritable() = %v, want nil", err)
	}

	// The probe must not be left behind.
	entries, err := os.ReadDir(filepath.Join(dir, podcastsDir))
	if err != nil {
		t.Fatalf("read podcasts dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("probe file still present: %v", entries)
	}
}

func TestCheckDataDirWritableReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	dir := readOnlyPodcastsDir(t)

	if err := CheckDataDirWritable(dir); err == nil {
		t.Error("CheckDataDirWritable() = nil, want error for read-only dir")
	}
}

// Regression test: a leftover probe from a pre-issue-#8 version must not make a
// writable directory look unwritable, since server.Run exits on that error.
func TestCheckDataDirWritableIgnoresStaleProbe(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, podcasts string)
	}{
		{
			name: "unreadable leftover file",
			setup: func(t *testing.T, podcasts string) {
				if err := os.WriteFile(filepath.Join(podcasts, ".healthcheck"), nil, 0400); err != nil {
					t.Fatalf("write stale probe: %v", err)
				}
			},
		},
		{
			name: "leftover path is a directory",
			setup: func(t *testing.T, podcasts string) {
				if err := os.MkdirAll(filepath.Join(podcasts, ".healthcheck"), 0755); err != nil {
					t.Fatalf("mkdir stale probe: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			podcasts := filepath.Join(dir, podcastsDir)
			if err := os.MkdirAll(podcasts, 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			tt.setup(t, podcasts)

			if err := CheckDataDirWritable(dir); err != nil {
				t.Errorf("CheckDataDirWritable() = %v, want nil despite stale probe", err)
			}
		})
	}
}

// readOnlyPodcastsDir returns a data dir whose podcasts/ subdirectory rejects
// writes. The cleanup chmod is load-bearing: without it t.TempDir's RemoveAll
// cannot unlink the directory's contents and fails the test.
func readOnlyPodcastsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	podcasts := filepath.Join(dir, podcastsDir)
	if err := os.MkdirAll(podcasts, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(podcasts, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(podcasts, 0755) })
	return dir
}

// The conditional-request validators must survive a save/load cycle, since a
// restart otherwise refetches every feed in full.
func TestSaveAndLoadMetaValidators(t *testing.T) {
	dir := t.TempDir()
	original := &PodcastMeta{
		FeedURL:      "https://example.com/feed.xml",
		Title:        "Validators",
		AddedAt:      time.Now().Truncate(time.Second),
		ETag:         `W/"694005d4b2cf6d966ab8b63d882c2959"`,
		LastModified: "Thu, 13 Aug 2026 20:41:57 GMT",
	}

	if err := SaveMeta(dir, original); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	loaded, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}

	if loaded.ETag != original.ETag {
		t.Errorf("ETag = %q, want %q", loaded.ETag, original.ETag)
	}
	if loaded.LastModified != original.LastModified {
		t.Errorf("LastModified = %q, want %q", loaded.LastModified, original.LastModified)
	}
}

// Meta without validators must not write the keys at all, so a file written by
// an older version round-trips unchanged.
func TestSaveMetaOmitsEmptyValidators(t *testing.T) {
	dir := t.TempDir()
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/feed.xml", Title: "No Validators"}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, metaFilename))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	for _, key := range []string{"etag", "last_modified"} {
		if strings.Contains(string(data), key) {
			t.Errorf("meta contains %q key when validator is empty:\n%s", key, data)
		}
	}
}

// Meta written before the LastChangedAt rename carries last_checked_at. It is
// the closest thing to a change time those files have, so it must be adopted
// rather than leaving the field zero and showing nothing in the UI.
func TestLoadMetaAdoptsLegacyLastCheckedAt(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "feed_url": "https://example.com/feed.xml",
  "title": "Legacy",
  "last_checked_at": "2026-08-01T12:00:00Z"
}
`
	if err := os.WriteFile(filepath.Join(dir, metaFilename), []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy meta: %v", err)
	}

	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}

	want := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if !meta.LastChangedAt.Equal(want) {
		t.Errorf("LastChangedAt = %v, want %v adopted from last_checked_at", meta.LastChangedAt, want)
	}
}

// A file carrying both keys must prefer the current one.
func TestLoadMetaPrefersLastChangedAtOverLegacy(t *testing.T) {
	dir := t.TempDir()
	both := `{
  "feed_url": "https://example.com/feed.xml",
  "title": "Both",
  "last_checked_at": "2026-08-01T12:00:00Z",
  "last_changed_at": "2026-08-15T09:30:00Z"
}
`
	if err := os.WriteFile(filepath.Join(dir, metaFilename), []byte(both), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}

	want := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)
	if !meta.LastChangedAt.Equal(want) {
		t.Errorf("LastChangedAt = %v, want %v", meta.LastChangedAt, want)
	}
}

func TestSaveAndLoadMetaLastChangedAt(t *testing.T) {
	dir := t.TempDir()
	want := time.Now().UTC().Truncate(time.Second)
	if err := SaveMeta(dir, &PodcastMeta{FeedURL: "https://example.com/f.xml", Title: "T", LastChangedAt: want}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if !meta.LastChangedAt.Equal(want) {
		t.Errorf("LastChangedAt = %v, want %v", meta.LastChangedAt, want)
	}
}

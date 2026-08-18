package podstash

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	metaFilename  = ".podstash.meta.json"
	indexFilename = ".podstash.index.json"
	podcastsDir   = "podcasts"
	maxSlugLen    = 80
)

var multiHyphenRe = regexp.MustCompile(`-{2,}`)

// PodcastMeta holds podcast-level metadata, stored in .podstash.meta.json.
type PodcastMeta struct {
	FeedURL     string    `json:"feed_url"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Description string    `json:"description"`
	ImageURL    string    `json:"image_url"`
	AddedAt     time.Time `json:"added_at"`
	Paused      bool      `json:"paused"`

	// LastChangedAt is when the feed last returned something new, not when it
	// was last polled. A per-poll timestamp would rewrite this file on every
	// poll even for a dormant feed, which blocks disk spindown (issue #8).
	// The poll time is tracked in memory by PollHeartbeat instead.
	LastChangedAt time.Time `json:"last_changed_at,omitzero"`

	// SkipPatterns is a list of regex patterns. Episodes whose title or
	// description matches any pattern are recorded in the index but skipped
	// for download. Useful for filtering rebroadcasts, "best of" reruns, etc.
	SkipPatterns []string `json:"skip_patterns,omitzero"`

	// DownloadAfter filters episodes by publish date. Episodes published
	// before this date are recorded in the index but not downloaded.
	// Zero value means no date filter (download all).
	DownloadAfter *time.Time `json:"download_after,omitzero"`

	// ETag and LastModified are the HTTP cache validators the feed server sent
	// with the last successful fetch. They are echoed back on the next poll so
	// an unchanged feed can answer 304 instead of resending the document.
	ETag         string `json:"etag,omitzero"`
	LastModified string `json:"last_modified,omitzero"`

	// Slug is the directory name, derived from Title. Not stored in JSON.
	Slug string `json:"-"`
}

// EpisodeEntry represents a single episode in the index.
type EpisodeEntry struct {
	GUID          string    `json:"guid"`
	Title         string    `json:"title"`
	PubDate       time.Time `json:"pub_date"`
	EnclosureURL  string    `json:"enclosure_url"`
	EnclosureType string    `json:"enclosure_type"`
	Description   string    `json:"description"`
	Filename      string    `json:"filename,omitzero"`
	DownloadedAt  time.Time `json:"downloaded_at,omitzero"`
	FileSize      int64     `json:"file_size,omitzero"`
	Skipped       bool      `json:"skipped,omitzero"`
}

// EpisodeIndex is the list of all known episodes for a podcast.
type EpisodeIndex struct {
	Episodes []EpisodeEntry `json:"episodes"`
}

// podcastMutexes provides per-podcast locking to prevent concurrent writes.
//
// Entries are reference counted so a slug that is deleted, or simply never
// touched again, does not occupy the map for the life of the process. Handing
// out a bare *sync.Mutex would make that unsafe: a caller holding the pointer
// while the entry was removed would lock an orphan while the next caller
// created and locked a fresh one, and the two would not exclude each other.
// Locking and unlocking therefore go through lockPodcast, which is the only
// thing that adjusts the count.
var podcastMutexes = struct {
	mu sync.Mutex
	m  map[string]*podcastLock
}{m: make(map[string]*podcastLock)}

type podcastLock struct {
	mu sync.Mutex
	// waiters counts holders plus goroutines blocked on mu, guarded by
	// podcastMutexes.mu. The entry is removed when it reaches zero.
	waiters int
}

// lockPodcast locks the podcast's mutex and returns the function that unlocks
// it, normally used as `defer lockPodcast(slug)()`, which locks now and
// unlocks on return.
//
// The returned function ignores every call after the first. Without that, a
// second call while another goroutine held the same slug would release that
// goroutine's lock, since sync.Mutex tracks no ownership, and drop the
// reference count to zero so a third caller could enter the critical section
// alongside the holder. No current caller can do this, but the failure is
// silent corruption of exactly what the lock protects, so it is cheaper to
// make the API misuse-resistant than to rely on every future caller.
func lockPodcast(slug string) func() {
	podcastMutexes.mu.Lock()
	l, ok := podcastMutexes.m[slug]
	if !ok {
		l = &podcastLock{}
		podcastMutexes.m[slug] = l
	}
	l.waiters++
	podcastMutexes.mu.Unlock()

	l.mu.Lock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Unlock()

			podcastMutexes.mu.Lock()
			defer podcastMutexes.mu.Unlock()
			l.waiters--
			// Only drop the entry once nobody holds or awaits it, so no goroutine
			// can still be using this instance when a later caller makes a new one.
			if l.waiters == 0 && podcastMutexes.m[slug] == l {
				delete(podcastMutexes.m, slug)
			}
		})
	}
}

// podcastLockCount reports how many per-podcast locks are currently tracked.
// Used by tests to assert the map does not grow without bound.
func podcastLockCount() int {
	podcastMutexes.mu.Lock()
	defer podcastMutexes.mu.Unlock()
	return len(podcastMutexes.m)
}

// PodcastDir returns the full path to a podcast's directory.
func PodcastDir(dataDir, slug string) string {
	return filepath.Join(dataDir, podcastsDir, slug)
}

// LoadMeta reads the .podstash.meta.json file from the given podcast directory.
func LoadMeta(dir string) (*PodcastMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, metaFilename))
	if err != nil {
		return nil, fmt.Errorf("load meta: %w", err)
	}
	var meta PodcastMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}

	// Meta written before LastChangedAt existed carries last_checked_at, which
	// was set on every poll. It is the closest thing to a change time those
	// files have, so adopt it rather than showing the zero time. The adopted
	// value is rewritten under the current key whenever something next saves
	// meta, which for a quiet feed may not be soon: quiet polls no longer
	// write.
	if meta.LastChangedAt.IsZero() {
		var legacy struct {
			LastCheckedAt time.Time `json:"last_checked_at"`
		}
		// The outer Unmarshal already accepted this document, so a failure here
		// only means the legacy key is absent or malformed, which leaves
		// LastCheckedAt at the zero value we would fall back to anyway.
		_ = json.Unmarshal(data, &legacy)
		meta.LastChangedAt = legacy.LastCheckedAt
	}

	meta.Slug = filepath.Base(dir)
	return &meta, nil
}

// SaveMeta atomically writes the .podstash.meta.json file.
func SaveMeta(dir string, meta *PodcastMeta) error {
	return atomicWriteJSON(filepath.Join(dir, metaFilename), meta)
}

// LoadIndex reads the .podstash.index.json file from the given podcast directory.
func LoadIndex(dir string) (*EpisodeIndex, error) {
	path := filepath.Join(dir, indexFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &EpisodeIndex{}, nil
		}
		return nil, fmt.Errorf("load index: %w", err)
	}
	var idx EpisodeIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	return &idx, nil
}

// SaveIndex atomically writes the .podstash.index.json file.
func SaveIndex(dir string, idx *EpisodeIndex) error {
	return atomicWriteJSON(filepath.Join(dir, indexFilename), idx)
}

// ListPodcasts scans the data directory and loads metadata for all podcasts.
func ListPodcasts(dataDir string) ([]PodcastMeta, error) {
	root := filepath.Join(dataDir, podcastsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list podcasts: %w", err)
	}

	var podcasts []PodcastMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		meta, err := LoadMeta(dir)
		if err != nil {
			continue // skip directories without valid metadata
		}
		podcasts = append(podcasts, *meta)
	}
	return podcasts, nil
}

// HasGUID checks whether an episode with the given GUID exists in the index.
func (idx *EpisodeIndex) HasGUID(guid string) bool {
	for _, ep := range idx.Episodes {
		if ep.GUID == guid {
			return true
		}
	}
	return false
}

// CompileSkipPatterns compiles the skip patterns from a PodcastMeta.
// Invalid patterns are silently skipped.
func CompileSkipPatterns(patterns []string) []*regexp.Regexp {
	var compiled []*regexp.Regexp
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled
}

// MatchesSkipPattern returns true if the episode title or description
// matches any of the compiled skip patterns.
func MatchesSkipPattern(patterns []*regexp.Regexp, title, description string) bool {
	for _, re := range patterns {
		if re.MatchString(title) || re.MatchString(description) {
			return true
		}
	}
	return false
}

// sanitizeName normalizes a string into a filesystem-safe kebab-case form.
// Used by both Slugify (for directory names) and SanitizeFilename (for episode files).
func sanitizeName(s string, fallback string, maxLen int) string {
	s = norm.NFD.String(s)
	var b strings.Builder
	for _, r := range s {
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_') {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	s = strings.TrimSpace(b.String())
	s = strings.ReplaceAll(s, " ", "-")
	s = multiHyphenRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if s == "" {
		s = fallback
	}
	if len(s) > maxLen {
		s = s[:maxLen]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// Slugify converts a title into a filesystem-safe kebab-case directory name.
func Slugify(title string) string {
	return sanitizeName(title, "podcast", maxSlugLen)
}

// ValidSlug returns true if slug is safe to use as a path component.
func ValidSlug(slug string) bool {
	if slug == "" || slug == "." || slug == ".." {
		return false
	}
	return !strings.ContainsAny(slug, "/\\")
}

// CheckDataDirWritable verifies the data directory accepts writes, by creating
// and removing a probe file. Called once at startup: a data directory that is
// missing or read-only is a misconfigured mount, and failing immediately beats
// surfacing it later as a failed download.
//
// The probe name is unique per call, so a leftover from an earlier run can
// never make a writable directory look unwritable and abort startup.
func CheckDataDirWritable(dataDir string) error {
	f, err := os.CreateTemp(filepath.Join(dataDir, podcastsDir), ".podstash-writecheck-*")
	if err != nil {
		return fmt.Errorf("data dir not writable: %w", err)
	}
	probe := f.Name()
	if err := f.Close(); err != nil {
		// Best effort: the probe exists, so remove it before reporting.
		_ = os.Remove(probe)
		return fmt.Errorf("close write probe: %w", err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("remove write probe: %w", err)
	}
	return nil
}

// atomicWriteJSON marshals v to JSON and writes it atomically to path.
//
// The write is skipped when the file already holds identical bytes. Every write
// dirties the containing directory and forces a filesystem journal commit, which
// on spinning disks keeps the drive awake; most polls change nothing, so
// comparing first lets an idle library disk spin down. See issue #8.
func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')

	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

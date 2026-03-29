package podstash

import (
	"encoding/json"
	"fmt"
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
	FeedURL       string    `json:"feed_url"`
	Title         string    `json:"title"`
	Author        string    `json:"author"`
	Description   string    `json:"description"`
	ImageURL      string    `json:"image_url"`
	AddedAt       time.Time `json:"added_at"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	Paused        bool      `json:"paused"`

	// SkipPatterns is a list of regex patterns. Episodes whose title or
	// description matches any pattern are recorded in the index but skipped
	// for download. Useful for filtering rebroadcasts, "best of" reruns, etc.
	SkipPatterns []string `json:"skip_patterns,omitzero"`

	// DownloadAfter filters episodes by publish date. Episodes published
	// before this date are recorded in the index but not downloaded.
	// Zero value means no date filter (download all).
	DownloadAfter *time.Time `json:"download_after,omitzero"`

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
var podcastMutexes sync.Map

func podcastMu(slug string) *sync.Mutex {
	v, _ := podcastMutexes.LoadOrStore(slug, &sync.Mutex{})
	return v.(*sync.Mutex)
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
		if os.IsNotExist(err) {
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
		if os.IsNotExist(err) {
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

// atomicWriteJSON marshals v to JSON and writes it atomically to path.
func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')

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

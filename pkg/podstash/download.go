package podstash

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxFileNameLen = 80
	coverFilename  = "cover.jpg"
)

// SanitizeFilename converts a title into a filesystem-safe string.
func SanitizeFilename(title string) string {
	return sanitizeName(title, "episode", maxFileNameLen)
}

// FileExtFromURL extracts the file extension from an enclosure URL.
// Falls back to ".mp3" if none can be determined.
func FileExtFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ".mp3"
	}
	ext := path.Ext(u.Path)
	if ext == "" {
		return ".mp3"
	}
	return ext
}

// EpisodeFilename generates a filename for an episode.
// Format: {YYYY-MM-DD}-{sanitized-title}.{ext}
func EpisodeFilename(ep *EpisodeEntry) string {
	datePart := "0000-00-00"
	if !ep.PubDate.IsZero() {
		datePart = ep.PubDate.Format("2006-01-02")
	}
	titlePart := SanitizeFilename(ep.Title)
	ext := FileExtFromURL(ep.EnclosureURL)
	return fmt.Sprintf("%s-%s%s", datePart, titlePart, ext)
}

// DownloadEpisode downloads a single episode to the podcast directory.
// It writes to a temp file first, then renames for atomicity.
// If meta is provided, ID3 tags are written to MP3 files after download.
func DownloadEpisode(client HTTPClient, podcastDir string, ep *EpisodeEntry, meta *PodcastMeta) error {
	if ep.Filename != "" {
		return nil // already downloaded
	}
	if ep.EnclosureURL == "" {
		return fmt.Errorf("episode %q has no enclosure URL", ep.Title)
	}

	filename := EpisodeFilename(ep)
	destPath := filepath.Join(podcastDir, filename)

	// Check if file already exists on disk (e.g., from previous tool).
	if info, err := os.Stat(destPath); err == nil {
		ep.Filename = filename
		ep.DownloadedAt = time.Now().UTC()
		ep.FileSize = info.Size()
		return nil
	}

	resp, err := client.Get(ep.EnclosureURL)
	if err != nil {
		return fmt.Errorf("download %q: %w", ep.Title, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %q: status %d", ep.Title, resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	n, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write %q: %w", ep.Title, copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close %q: %w", ep.Title, closeErr)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %q: %w", ep.Title, err)
	}

	ep.Filename = filename
	ep.DownloadedAt = time.Now().UTC()
	ep.FileSize = n

	// Tag MP3 files with episode metadata.
	if meta != nil {
		coverPath := filepath.Join(podcastDir, coverFilename)
		if _, err := os.Stat(coverPath); err != nil {
			coverPath = "" // no cover available
		}
		if err := TagMP3(destPath, meta, ep, coverPath); err != nil {
			slog.Warn("id3 tag failed", "episode", ep.Title, "error", err)
		}
	}

	return nil
}

// DownloadCoverImage downloads the podcast cover image if not already present.
func DownloadCoverImage(client HTTPClient, podcastDir string, imageURL string) error {
	if imageURL == "" {
		return nil
	}

	destPath := filepath.Join(podcastDir, coverFilename)
	if _, err := os.Stat(destPath); err == nil {
		return nil // already exists
	}

	resp, err := client.Get(imageURL)
	if err != nil {
		return fmt.Errorf("download cover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download cover: status %d", resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create cover temp file: %w", err)
	}

	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write cover: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close cover: %w", closeErr)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename cover: %w", err)
	}
	return nil
}

// DownloadPending downloads all episodes that haven't been downloaded yet,
// across all podcasts. Uses a semaphore for bounded concurrency.
func DownloadPending(client HTTPClient, dataDir string, workers int) error {
	podcasts, err := ListPodcasts(dataDir)
	if err != nil {
		return err
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, p := range podcasts {
		if p.Paused {
			continue
		}
		wg.Add(1)
		go func(slug string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			downloadPodcastEpisodes(client, PodcastDir(dataDir, slug), slug)
		}(p.Slug)
	}

	wg.Wait()
	return nil
}

// downloadPodcastEpisodes downloads all pending episodes for a single podcast.
func downloadPodcastEpisodes(client HTTPClient, dir, slug string) {
	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	meta, err := LoadMeta(dir)
	if err != nil {
		slog.Error("load meta failed", "podcast", slug, "error", err)
		return
	}

	// Download cover image if not already present.
	if meta.ImageURL != "" {
		if err := DownloadCoverImage(client, dir, meta.ImageURL); err != nil {
			slog.Warn("cover download failed", "podcast", slug, "error", err)
		}
	}

	idx, err := LoadIndex(dir)
	if err != nil {
		slog.Error("load index failed", "podcast", slug, "error", err)
		return
	}

	changed := false
	for i := range idx.Episodes {
		ep := &idx.Episodes[i]
		if ep.Filename != "" || ep.EnclosureURL == "" || ep.Skipped {
			continue
		}
		if err := DownloadEpisode(client, dir, ep, meta); err != nil {
			slog.Warn("episode download failed", "podcast", slug, "episode", ep.Title, "error", err)
			continue
		}
		slog.Info("episode downloaded", "podcast", slug, "episode", ep.Title)
		changed = true
	}

	if changed {
		if err := SaveIndex(dir, idx); err != nil {
			slog.Error("save index failed", "podcast", slug, "error", err)
		}
	}
}

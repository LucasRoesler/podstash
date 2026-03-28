package podstash

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// App holds the application dependencies.
type App struct {
	DataDir         string
	Client          HTTPClient
	Tmpl            map[string]*template.Template
	DownloadWorkers int
}

// PodcastView holds data for rendering a podcast in templates.
type PodcastView struct {
	Meta               PodcastMeta
	TotalEpisodes      int
	DownloadedEpisodes int
}

// PodcastDetailView holds data for the podcast detail page.
type PodcastDetailView struct {
	Meta               PodcastMeta
	TotalEpisodes      int
	DownloadedEpisodes int
	SkippedEpisodes    int
	Episodes           []EpisodeEntry
}

// HomeData holds data for the home page.
type HomeData struct {
	Podcasts []PodcastView
}

// AddData holds data for the add page.
type AddData struct {
	Error   string
	Success string
}

// slugParam extracts and validates the slug path parameter.
// Returns the slug and true if valid, or writes a 404 and returns false.
func slugParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	slug := r.PathValue("slug")
	if !ValidSlug(slug) {
		http.NotFound(w, r)
		return "", false
	}
	return slug, true
}

func (app *App) handleHome(w http.ResponseWriter, r *http.Request) {
	podcasts, err := ListPodcasts(app.DataDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var views []PodcastView
	for _, p := range podcasts {
		dir := PodcastDir(app.DataDir, p.Slug)
		idx, _ := LoadIndex(dir)
		total := len(idx.Episodes)
		downloaded := 0
		for _, ep := range idx.Episodes {
			if ep.Filename != "" {
				downloaded++
			}
		}
		views = append(views, PodcastView{
			Meta:               p,
			TotalEpisodes:      total,
			DownloadedEpisodes: downloaded,
		})
	}

	app.render(w, "home.html", HomeData{Podcasts: views})
}

func (app *App) handlePodcast(w http.ResponseWriter, r *http.Request) {
	slug, ok := slugParam(w, r)
	if !ok {
		return
	}
	dir := PodcastDir(app.DataDir, slug)

	meta, err := LoadMeta(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	idx, err := LoadIndex(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	downloaded, skipped := 0, 0
	for _, ep := range idx.Episodes {
		if ep.Filename != "" {
			downloaded++
		}
		if ep.Skipped {
			skipped++
		}
	}

	app.render(w, "podcast.html", PodcastDetailView{
		Meta:               *meta,
		TotalEpisodes:      len(idx.Episodes),
		DownloadedEpisodes: downloaded,
		SkippedEpisodes:    skipped,
		Episodes:           idx.Episodes,
	})
}

func (app *App) handleAddPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, "add.html", AddData{})
}

func (app *App) handleAddPodcast(w http.ResponseWriter, r *http.Request) {
	feedURL := r.FormValue("url")
	if feedURL == "" {
		app.render(w, "add.html", AddData{Error: "Feed URL is required."})
		return
	}

	feed, err := FetchFeed(app.Client, feedURL)
	if err != nil {
		app.render(w, "add.html", AddData{Error: fmt.Sprintf("Failed to fetch feed: %v", err)})
		return
	}

	slug := Slugify(feed.Channel.Title)
	dir := PodcastDir(app.DataDir, slug)

	// Check if already exists.
	if _, err := os.Stat(filepath.Join(dir, metaFilename)); err == nil {
		app.render(w, "add.html", AddData{Error: fmt.Sprintf("Podcast %q already exists.", feed.Channel.Title)})
		return
	}

	os.MkdirAll(dir, 0755)

	meta := &PodcastMeta{
		FeedURL:     feedURL,
		Title:       feed.Channel.Title,
		Author:      feed.Channel.Author(),
		Description: feed.Channel.Description,
		ImageURL:    feed.Channel.ImageURL(),
		AddedAt:     time.Now().UTC(),
	}
	if err := SaveMeta(dir, meta); err != nil {
		app.render(w, "add.html", AddData{Error: fmt.Sprintf("Failed to save: %v", err)})
		return
	}

	// Save initial empty index and refresh immediately.
	SaveIndex(dir, &EpisodeIndex{})

	added, err := RefreshPodcast(app.Client, app.DataDir, slug)
	if err != nil {
		slog.Error("initial refresh failed", "podcast", slug, "error", err)
	} else {
		slog.Info("initial refresh complete", "podcast", slug, "added", added)
	}

	http.Redirect(w, r, "/podcasts/"+slug, http.StatusSeeOther)
}

func (app *App) handleDeletePodcast(w http.ResponseWriter, r *http.Request) {
	slug, ok := slugParam(w, r)
	if !ok {
		return
	}
	dir := PodcastDir(app.DataDir, slug)

	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	if _, err := LoadMeta(dir); err != nil {
		http.NotFound(w, r)
		return
	}

	if err := os.RemoveAll(dir); err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("podcast deleted", "podcast", slug)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *App) handleRefreshPodcast(w http.ResponseWriter, r *http.Request) {
	slug, ok := slugParam(w, r)
	if !ok {
		return
	}

	// Verify the podcast exists before launching background work.
	dir := PodcastDir(app.DataDir, slug)
	if _, err := LoadMeta(dir); err != nil {
		http.NotFound(w, r)
		return
	}

	go func() {
		added, err := RefreshPodcast(app.Client, app.DataDir, slug)
		if err != nil {
			slog.Error("refresh failed", "podcast", slug, "error", err)
			return
		}
		slog.Info("refresh complete", "podcast", slug, "added", added)
	}()

	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (app *App) handlePausePodcast(w http.ResponseWriter, r *http.Request) {
	slug, ok := slugParam(w, r)
	if !ok {
		return
	}
	dir := PodcastDir(app.DataDir, slug)

	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	meta, err := LoadMeta(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	meta.Paused = !meta.Paused
	if err := SaveMeta(dir, meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (app *App) handleAddSkipPattern(w http.ResponseWriter, r *http.Request) {
	slug, ok := slugParam(w, r)
	if !ok {
		return
	}
	dir := PodcastDir(app.DataDir, slug)

	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	meta, err := LoadMeta(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	pattern := r.FormValue("pattern")
	if pattern == "" {
		http.Redirect(w, r, "/podcasts/"+slug, http.StatusSeeOther)
		return
	}

	// Validate the regex.
	if _, err := regexp.Compile(pattern); err != nil {
		http.Error(w, fmt.Sprintf("Invalid regex: %v", err), http.StatusBadRequest)
		return
	}

	meta.SkipPatterns = append(meta.SkipPatterns, pattern)
	if err := SaveMeta(dir, meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/podcasts/"+slug, http.StatusSeeOther)
}

func (app *App) handleDeleteSkipPattern(w http.ResponseWriter, r *http.Request) {
	slug, ok := slugParam(w, r)
	if !ok {
		return
	}
	dir := PodcastDir(app.DataDir, slug)

	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	meta, err := LoadMeta(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	idxStr := r.FormValue("index")
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(meta.SkipPatterns) {
		http.Error(w, "Invalid pattern index", http.StatusBadRequest)
		return
	}

	meta.SkipPatterns = append(meta.SkipPatterns[:idx], meta.SkipPatterns[idx+1:]...)
	if err := SaveMeta(dir, meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/podcasts/"+slug, http.StatusSeeOther)
}

func (app *App) handleOPMLImport(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("opml")
	if err != nil {
		app.render(w, "add.html", AddData{Error: "Failed to read OPML file."})
		return
	}
	defer file.Close()

	urls, err := ParseOPML(file)
	if err != nil {
		app.render(w, "add.html", AddData{Error: fmt.Sprintf("Failed to parse OPML: %v", err)})
		return
	}

	// Process feeds sequentially — simple, bounded, no goroutine explosion.
	added := 0
	for _, feedURL := range urls {
		feed, err := FetchFeed(app.Client, feedURL)
		if err != nil {
			slog.Warn("opml import: feed skipped", "url", feedURL, "error", err)
			continue
		}

		slug := Slugify(feed.Channel.Title)
		dir := PodcastDir(app.DataDir, slug)

		if _, err := os.Stat(filepath.Join(dir, metaFilename)); err == nil {
			continue // already exists
		}

		if err := os.MkdirAll(dir, 0755); err != nil {
			slog.Error("opml import: mkdir failed", "podcast", slug, "error", err)
			continue
		}
		meta := &PodcastMeta{
			FeedURL:     feedURL,
			Title:       feed.Channel.Title,
			Author:      feed.Channel.Author(),
			Description: feed.Channel.Description,
			ImageURL:    feed.Channel.ImageURL(),
			AddedAt:     time.Now().UTC(),
		}
		if err := SaveMeta(dir, meta); err != nil {
			slog.Error("opml import: save failed", "podcast", slug, "error", err)
			continue
		}
		SaveIndex(dir, &EpisodeIndex{})
		added++
		slog.Info("opml import: added", "podcast", slug)
	}

	// Refreshes happen on the next poll cycle — no fire-and-forget goroutines.
	app.render(w, "add.html", AddData{
		Success: fmt.Sprintf("Imported %d of %d feeds. Episodes will download on next poll.", added, len(urls)),
	})
}

func (app *App) handleOPMLExport(w http.ResponseWriter, r *http.Request) {
	podcasts, err := ListPodcasts(app.DataDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data, err := ExportOPML(podcasts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", "attachment; filename=podstash.opml")
	w.Write(data)
}

func (app *App) render(w http.ResponseWriter, name string, data any) {
	t, ok := app.Tmpl[name]
	if !ok {
		slog.Error("template not found", "template", name)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		slog.Error("template render failed", "template", name, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

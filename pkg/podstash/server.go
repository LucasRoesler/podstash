package podstash

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

//go:embed templates/*
var templateFiles embed.FS

// Run starts the podstash server. It blocks until the server is stopped.
func Run(cfg Config) {
	InitLogger(cfg.LogFormat)

	slog.Info("podstash starting", "data", cfg.DataDir, "port", cfg.Port, "poll", cfg.PollInterval)

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, podcastsDir), 0755); err != nil {
		slog.Error("create data dir failed", "error", err)
		os.Exit(1)
	}

	app := &App{
		DataDir:         cfg.DataDir,
		Client:          &http.Client{Timeout: cfg.HTTPTimeout},
		Tmpl:            loadTemplates(),
		DownloadWorkers: cfg.DownloadWorkers,
	}

	mux := http.NewServeMux()

	// Static files.
	// Ignore error — path is a compile-time constant embedded via go:embed.
	staticSub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Pages.
	mux.HandleFunc("GET /{$}", app.handleHome)
	mux.HandleFunc("GET /podcasts/{slug}", app.handlePodcast)
	mux.HandleFunc("GET /add", app.handleAddPage)

	// Podcast client integration.
	mux.HandleFunc("GET /podcasts/{slug}/feed.xml", app.handlePodcastFeed)
	mux.HandleFunc("GET /podcasts/{slug}/cover.jpg", app.handleServeCover)
	mux.HandleFunc("GET /podcasts/{slug}/episodes/{filename}", app.handleServeEpisode)

	// Actions.
	mux.HandleFunc("POST /podcasts", app.handleAddPodcast)
	mux.HandleFunc("POST /podcasts/{slug}/delete", app.handleDeletePodcast)
	mux.HandleFunc("POST /podcasts/{slug}/refresh", app.handleRefreshPodcast)
	mux.HandleFunc("POST /podcasts/{slug}/pause", app.handlePausePodcast)
	mux.HandleFunc("POST /podcasts/{slug}/download-after", app.handleSetDownloadAfter)
	mux.HandleFunc("POST /podcasts/{slug}/skip", app.handleAddSkipPattern)
	mux.HandleFunc("POST /podcasts/{slug}/skip/delete", app.handleDeleteSkipPattern)
	mux.HandleFunc("POST /opml", app.handleOPMLImport)
	mux.HandleFunc("GET /opml", app.handleOPMLExport)
	mux.HandleFunc("GET /healthz", app.handleHealthz)

	// Create a cancellable context for background work.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start poller.
	go startPoller(ctx, app, cfg.PollInterval)

	// Start server with graceful shutdown.
	addr := ":" + cfg.Port
	srv := &http.Server{Addr: addr, Handler: requestLogger(mux)}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig)
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		// Ignore error — we're shutting down, best effort.
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start).Round(time.Millisecond),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func startPoller(ctx context.Context, app *App, interval time.Duration) {
	pollOnce(app)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopped")
			return
		case <-ticker.C:
			pollOnce(app)
		}
	}
}

func pollOnce(app *App) {
	slog.Info("poll: refreshing feeds")
	podcasts, err := ListPodcasts(app.DataDir)
	if err != nil {
		slog.Error("poll: list podcasts failed", "error", err)
		return
	}

	for _, p := range podcasts {
		if p.Paused {
			continue
		}
		added, err := RefreshPodcast(app.Client, app.DataDir, p.Slug)
		if err != nil {
			slog.Error("poll: refresh failed", "podcast", p.Slug, "error", err)
			continue
		}
		if added > 0 {
			slog.Info("poll: new episodes", "podcast", p.Slug, "added", added)
		}
	}

	slog.Info("poll: downloading pending episodes")
	if err := DownloadPending(app.Client, app.DataDir, app.DownloadWorkers); err != nil {
		slog.Error("poll: download failed", "error", err)
	}
	slog.Info("poll: done")
}

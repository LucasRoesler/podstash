# CLAUDE.md

## Project

Podstash is a self-contained podcast archiver written in Go. It subscribes to RSS feeds, downloads episodes to disk, and tags MP3 files with ID3v2 metadata. No database — all state is stored as JSON files alongside episode files on the filesystem.

## Build & Development Commands

Follow the development workflow and guidelines in @DEVELOPMENT.md

## Design Principles

1. **Clean Architecture**
   - Clear separation of concerns
   - Package-level organization by domain
   - Explicit dependencies via interfaces

2. **Dependency Injection**
   - All dependencies injected via constructors
   - Use interfaces for external dependencies
   - No global state or singletons

3. **Testable Code**
   - Small, focused functions
   - Interfaces for mockable dependencies
   - Pure functions where possible
   - Test coverage for all new code

4. **Minimal Dependencies**
   - Prefer the standard library when possible and add new local packages for utilities.
   - But we reasonable and add new dependencies when appropriate, for example:
     - use well known and validate security, auth, or encryption libraries instead of writing new packages.
     - use well known and tested libraries that implement large standards like interacting with MP3 tags, PDFs, Websockets, etc.
   - Be picky about adding new dependencies, check the quality and consider vendoring them if they are small complete and focused packages.

Follow the Go conventions in @conventions/go.md

## Architecture

Single Go package (`pkg/podstash/`) with a thin `main.go` entry point.

**Core flow:** `main.go` → `LoadConfig()` → `Run(cfg)` which sets up the HTTP server, routes, and a background poller that refreshes feeds and downloads episodes on a configurable interval.

**Key files:**
- `server.go` — HTTP server setup, routing, graceful shutdown, background polling
- `handlers.go` — All HTTP handlers (add/delete/refresh podcasts, OPML import/export, etc.)
- `feed.go` — RSS feed fetching and XML parsing (handles iTunes namespace, XML 1.1)
- `download.go` — Episode downloading with bounded concurrency (semaphore), atomic writes
- `store.go` — Filesystem persistence (JSON meta/index files), per-podcast mutexes, slugification
- `tags.go` — ID3v2 MP3 tagging
- `opml.go` — OPML import/export
- `config.go` — Configuration from environment variables
- `templates.go` — HTML template loading and helpers

**Data layout on disk:**
```
$DATA_DIR/podcasts/{slug}/
  .podstash.meta.json    # Podcast metadata (feed URL, title, skip patterns, etc.)
  .podstash.index.json   # Episode index (GUIDs, download status, filenames)
  cover.jpg              # Cover art
  2024-03-15-title.mp3   # Downloaded episodes
```

**Concurrency patterns:**
- Per-podcast mutexes for file writes (in `store.go`)
- Semaphore-bounded download workers
- Atomic file writes via temp file + rename

**Testing:** Table-driven tests, `t.TempDir()` for isolation, `HTTPClient` interface for mocking HTTP. Test fixtures live in `pkg/podstash/testdata/`.

## Configuration

All via environment variables, loaded once in `config.go`:

| Variable           | Default | Description                     |
| ------------------ | ------- | ------------------------------- |
| `DATA_DIR`         | `/data` | Root directory for podcast data |
| `PORT`             | `8080`  | HTTP listen port                |
| `POLL_INTERVAL`    | `60m`   | Feed refresh interval           |
| `DOWNLOAD_WORKERS` | `2`     | Max concurrent downloads        |
| `HTTP_TIMEOUT`     | `2m`    | HTTP request timeout            |
| `LOG_FORMAT`       | `text`  | `text` or `json`                |

## Dependencies

Minimal: `github.com/bogem/id3v2/v2` for MP3 tagging, `golang.org/x/text` for Unicode normalization. Go 1.25+.

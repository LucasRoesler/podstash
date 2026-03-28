# podstash

A simple podcast archiver. Subscribe to RSS feeds, download episodes, and store them on disk. No database -- all state is stored as JSON files alongside the downloaded episodes.

## Features

- Subscribe to podcasts via RSS feed URL or OPML import
- Automatic periodic feed refresh and episode download
- ID3v2 tagging of downloaded MP3s (title, artist, album, cover art)
- Skip patterns: regex filters to skip rebroadcasts or unwanted episodes
- Server-rendered web UI for managing subscriptions
- OPML export for portability
- Structured logging via `log/slog`

## Quick start

```bash
go run .
# Listening on :8080, data in /data
```

Open `http://localhost:8080` and add a podcast.

## Configuration

All configuration is via environment variables, read once at startup.

| Variable | Default | Description |
|---|---|---|
| `DATA_DIR` | `/data` | Root directory for podcast data. Episodes are stored under `$DATA_DIR/podcasts/<slug>/`. |
| `PORT` | `8080` | HTTP server listen port. |
| `POLL_INTERVAL` | `60m` | How often to refresh feeds and download new episodes. Accepts Go duration strings (e.g. `30m`, `2h`). |
| `DOWNLOAD_WORKERS` | `2` | Maximum number of concurrent episode downloads. |
| `HTTP_TIMEOUT` | `2m` | Timeout for outbound HTTP requests (feed fetches, episode downloads). Accepts Go duration strings. |
| `LOG_FORMAT` | `text` | Log output format. Set to `json` for structured JSON logs (useful in Docker/production). |

### Example

```bash
DATA_DIR=./testdata PORT=8080 POLL_INTERVAL=30m LOG_FORMAT=json go run .
```

## Data layout

```
$DATA_DIR/
  podcasts/
    the-daily/
      .podstash.meta.json     # podcast metadata (feed URL, title, author, skip patterns)
      .podstash.index.json    # episode index (GUIDs, download status)
      cover.jpg               # podcast cover art
      2024-03-15-episode-title.mp3
    hardcore-history/
      .podstash.meta.json
      .podstash.index.json
      cover.jpg
      2024-01-10-episode-name.mp3
```

No database. The filesystem is the source of truth. Back up the data directory and you have everything.

## Docker

```dockerfile
docker build -t podstash .
docker run -v /path/to/data:/data -p 8080:8080 podstash
```

### Docker Compose

```yaml
services:
  podstash:
    image: ghcr.io/lucasroesler/podstash:latest
    container_name: podstash
    restart: unless-stopped
    volumes:
      - /path/to/podcasts:/data/podcasts
    environment:
      - POLL_INTERVAL=60m
      - LOG_FORMAT=json
    ports:
      - "8080:8080"
```

## OPML

Import subscriptions from another podcast app:
1. Export OPML from your current app
2. Open podstash, go to "Add", upload the OPML file

Export subscriptions: `GET /opml` or click "Export OPML" in the nav bar.

## Skip patterns

Per-podcast regex filters to skip unwanted episodes. Matched against episode title and description. Useful for filtering rebroadcasts, "best of" compilations, or ad-only episodes.

Manage via the podcast detail page in the web UI.

Example patterns:
- `(?i)best\s+of` -- skip "Best Of" episodes (case-insensitive)
- `(?i)rebroadcast` -- skip rebroadcasts
- `(?i)ad\s*break` -- skip ad break episodes

## License

MIT

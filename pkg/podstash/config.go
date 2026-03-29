package podstash

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config holds application configuration from environment variables.
// All configuration is read once at startup via LoadConfig().
type Config struct {
	DataDir         string
	Port            string
	PollInterval    time.Duration
	LogFormat       string
	DownloadWorkers int
	HTTPTimeout     time.Duration
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() Config {
	cfg := Config{
		DataDir:         cmp.Or(os.Getenv("DATA_DIR"), "/data"),
		Port:            cmp.Or(os.Getenv("PORT"), "8080"),
		PollInterval:    60 * time.Minute,
		LogFormat:       cmp.Or(os.Getenv("LOG_FORMAT"), "text"),
		DownloadWorkers: 2,
		HTTPTimeout:     2 * time.Minute,
	}

	if s := os.Getenv("POLL_INTERVAL"); s != "" {
		cfg.PollInterval = parseDuration("POLL_INTERVAL", s)
	}
	if s := os.Getenv("DOWNLOAD_WORKERS"); s != "" {
		cfg.DownloadWorkers = parseInt("DOWNLOAD_WORKERS", s)
	}
	if s := os.Getenv("HTTP_TIMEOUT"); s != "" {
		cfg.HTTPTimeout = parseDuration("HTTP_TIMEOUT", s)
	}

	return cfg
}

func parseDuration(name, s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s %q: %v\n", name, s, err)
		os.Exit(1)
	}
	return d
}

func parseInt(name, s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s %q: %v\n", name, s, err)
		os.Exit(1)
	}
	if v < 1 {
		fmt.Fprintf(os.Stderr, "invalid %s %q: must be >= 1\n", name, s)
		os.Exit(1)
	}
	return v
}

// InitLogger sets up slog with the configured format.
func InitLogger(format string) {
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, nil)
	default:
		handler = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(handler))
}

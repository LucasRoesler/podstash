package podstash

import (
	"fmt"
	"html/template"
	"time"
)

// templateFuncs provides helper functions for templates.
var templateFuncs = template.FuncMap{
	"formatDate": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Format("2006-01-02")
	},
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return t.Format("2006-01-02 15:04")
	},
	"truncate": func(s string, max int) string {
		r := []rune(s)
		if len(r) <= max {
			return s
		}
		return string(r[:max]) + "..."
	},
	"formatSize": func(size int64) string {
		switch {
		case size >= 1<<30:
			return fmt.Sprintf("%.1f GB", float64(size)/(1<<30))
		case size >= 1<<20:
			return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
		case size >= 1<<10:
			return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
		default:
			return fmt.Sprintf("%d B", size)
		}
	},
}

// loadTemplates parses page templates, each combined with the layout.
// Returns a map of page name to template.
func loadTemplates() map[string]*template.Template {
	pages := []string{"home.html", "podcast.html", "add.html"}
	templates := make(map[string]*template.Template)

	for _, page := range pages {
		t := template.New("").Funcs(templateFuncs)
		t = template.Must(t.ParseFS(templateFiles, "templates/layout.html", "templates/"+page))
		templates[page] = t
	}
	return templates
}

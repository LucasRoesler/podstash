package main

import (
	"encoding/xml"
	"fmt"
	"io"
)

// OPML XML structures

type OPML struct {
	XMLName xml.Name    `xml:"opml"`
	Head    OPMLHead    `xml:"head"`
	Body    OPMLBody    `xml:"body"`
}

type OPMLHead struct {
	Title string `xml:"title"`
}

type OPMLBody struct {
	Outlines []OPMLOutline `xml:"outline"`
}

type OPMLOutline struct {
	Text     string        `xml:"text,attr"`
	Type     string        `xml:"type,attr"`
	XMLURL   string        `xml:"xmlUrl,attr"`
	HTMLURL  string        `xml:"htmlUrl,attr"`
	Children []OPMLOutline `xml:"outline"`
}

// ParseOPML reads an OPML document and extracts all RSS feed URLs.
// It handles nested outlines (grouped feeds).
func ParseOPML(r io.Reader) ([]string, error) {
	const maxOPMLSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(r, maxOPMLSize))
	if err != nil {
		return nil, fmt.Errorf("read opml: %w", err)
	}

	var opml OPML
	if err := xml.Unmarshal(data, &opml); err != nil {
		return nil, fmt.Errorf("parse opml: %w", err)
	}

	var urls []string
	collectURLs(opml.Body.Outlines, &urls)
	return urls, nil
}

func collectURLs(outlines []OPMLOutline, urls *[]string) {
	for _, o := range outlines {
		if o.XMLURL != "" {
			*urls = append(*urls, o.XMLURL)
		}
		if len(o.Children) > 0 {
			collectURLs(o.Children, urls)
		}
	}
}

// ExportOPML generates an OPML document from a list of podcasts.
func ExportOPML(podcasts []PodcastMeta) ([]byte, error) {
	opml := OPML{
		Head: OPMLHead{Title: "podstash subscriptions"},
	}

	for _, p := range podcasts {
		opml.Body.Outlines = append(opml.Body.Outlines, OPMLOutline{
			Text:   p.Title,
			Type:   "rss",
			XMLURL: p.FeedURL,
		})
	}

	data, err := xml.MarshalIndent(opml, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal opml: %w", err)
	}

	return append([]byte(xml.Header), data...), nil
}

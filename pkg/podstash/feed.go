package podstash

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPClient abstracts HTTP fetching for testability.
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

// RSS XML structures

type RSSFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel RSSChannel `xml:"channel"`
}

const itunesNS = "http://www.itunes.com/dtds/podcast-1.0.dtd"

type RSSChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	LastBuildDate string    `xml:"lastBuildDate"`
	Image         RSSImage  `xml:"-"`
	ITunesAuthor  string    `xml:"-"`
	ITunesImage   string    `xml:"-"`
	Items         []RSSItem `xml:"-"`
}

// UnmarshalXML handles the itunes:image vs image namespace conflict.
func (ch *RSSChannel) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Space == itunesNS && t.Name.Local == "image":
				for _, a := range t.Attr {
					if a.Name.Local == "href" {
						ch.ITunesImage = a.Value
					}
				}
				d.Skip()
			case t.Name.Space == itunesNS && t.Name.Local == "author":
				var s string
				d.DecodeElement(&s, &t)
				ch.ITunesAuthor = s
			case t.Name.Local == "image" && t.Name.Space == "":
				d.DecodeElement(&ch.Image, &t)
			case t.Name.Local == "item":
				var item RSSItem
				d.DecodeElement(&item, &t)
				ch.Items = append(ch.Items, item)
			case t.Name.Local == "title":
				d.DecodeElement(&ch.Title, &t)
			case t.Name.Local == "link" && t.Name.Space == "":
				d.DecodeElement(&ch.Link, &t)
			case t.Name.Local == "description":
				d.DecodeElement(&ch.Description, &t)
			case t.Name.Local == "language":
				d.DecodeElement(&ch.Language, &t)
			case t.Name.Local == "lastBuildDate":
				d.DecodeElement(&ch.LastBuildDate, &t)
			default:
				d.Skip()
			}
		case xml.EndElement:
			return nil
		}
	}
}

type RSSImage struct {
	URL string `xml:"url"`
}

type RSSITunesImage struct {
	Href string `xml:"href,attr"`
}

type RSSItem struct {
	Title          string         `xml:"title"`
	Description    string         `xml:"description"`
	Enclosure      RSSEnclosure   `xml:"enclosure"`
	GUID           string         `xml:"guid"`
	PubDate        string         `xml:"pubDate"`
	ITunesDuration string         `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	ITunesImage    RSSITunesImage `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
}

type RSSEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// ParseFeed parses RSS XML bytes into an RSSFeed.
func ParseFeed(data []byte) (*RSSFeed, error) {
	var feed RSSFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return &feed, nil
}

// FetchFeed downloads and parses an RSS feed from the given URL.
func FetchFeed(client HTTPClient, url string) (*RSSFeed, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch feed: status %d", resp.StatusCode)
	}

	const maxFeedSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize))
	if err != nil {
		return nil, fmt.Errorf("read feed body: %w", err)
	}
	return ParseFeed(data)
}

// Author returns the best available author string from the feed.
func (ch *RSSChannel) Author() string {
	return ch.ITunesAuthor
}

// ImageURL returns the best available image URL from the feed.
func (ch *RSSChannel) ImageURL() string {
	if ch.ITunesImage != "" {
		return ch.ITunesImage
	}
	if ch.Image.URL != "" {
		return ch.Image.URL
	}
	return ""
}

// ParsePubDate tries multiple date formats used in RSS feeds.
func ParsePubDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}

	formats := []string{
		time.RFC1123Z,                           // Mon, 02 Jan 2006 15:04:05 -0700
		time.RFC1123,                            // Mon, 02 Jan 2006 15:04:05 MST
		"Mon, 2 Jan 2006 15:04:05 -0700",        // single-digit day
		"Mon, 2 Jan 2006 15:04:05 MST",          // single-digit day, named tz
		time.RFC3339,                            // 2006-01-02T15:04:05Z07:00
		"2006-01-02T15:04:05Z",                  // RFC3339 without offset
		"2006-01-02 15:04:05",                   // common alternative
		"Mon, 02 Jan 2006 15:04:05 +0000 (UTC)", // some feeds append timezone name
	}

	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// RefreshPodcast fetches the RSS feed for a podcast and adds any new episodes
// to the index. Returns the number of new episodes added.
func RefreshPodcast(client HTTPClient, dataDir string, slug string) (int, error) {
	dir := PodcastDir(dataDir, slug)
	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	meta, err := LoadMeta(dir)
	if err != nil {
		return 0, fmt.Errorf("refresh %s: %w", slug, err)
	}

	feed, err := FetchFeed(client, meta.FeedURL)
	if err != nil {
		return 0, fmt.Errorf("refresh %s: %w", slug, err)
	}

	idx, err := LoadIndex(dir)
	if err != nil {
		return 0, fmt.Errorf("refresh %s: %w", slug, err)
	}

	// Update podcast metadata from feed.
	meta.Title = feed.Channel.Title
	if a := feed.Channel.Author(); a != "" {
		meta.Author = a
	}
	if desc := feed.Channel.Description; desc != "" {
		meta.Description = desc
	}
	if img := feed.Channel.ImageURL(); img != "" {
		meta.ImageURL = img
	}
	meta.LastCheckedAt = time.Now().UTC()

	// Compile skip patterns once for this refresh cycle.
	skipPatterns := CompileSkipPatterns(meta.SkipPatterns)

	// Add new episodes.
	added := 0
	for _, item := range feed.Channel.Items {
		guid := item.GUID
		if guid == "" {
			guid = item.Enclosure.URL // fallback
		}
		if guid == "" {
			continue
		}
		if idx.HasGUID(guid) {
			continue
		}

		ep := EpisodeEntry{
			GUID:          guid,
			Title:         item.Title,
			PubDate:       ParsePubDate(item.PubDate),
			EnclosureURL:  item.Enclosure.URL,
			EnclosureType: item.Enclosure.Type,
			Description:   item.Description,
		}

		// Mark as skipped if filtered by pattern or date.
		if MatchesSkipPattern(skipPatterns, item.Title, item.Description) {
			ep.Skipped = true
		} else if meta.DownloadAfter != nil && !ep.PubDate.IsZero() && ep.PubDate.Before(*meta.DownloadAfter) {
			ep.Skipped = true
		}

		idx.Episodes = append(idx.Episodes, ep)
		added++
	}

	if err := SaveMeta(dir, meta); err != nil {
		return added, fmt.Errorf("refresh %s: %w", slug, err)
	}
	if err := SaveIndex(dir, idx); err != nil {
		return added, fmt.Errorf("refresh %s: %w", slug, err)
	}

	return added, nil
}

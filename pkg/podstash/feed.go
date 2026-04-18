package podstash

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// xmlDeclRe matches XML declarations with version > 1.0.
// Go's encoding/xml only supports version="1.0".
var xmlDeclRe = regexp.MustCompile(`<\?xml\s+version="1\.[1-9]"`)

// sanitizeXML rewrites XML 1.1+ declarations to 1.0 so encoding/xml can parse them.
func sanitizeXML(data []byte) []byte {
	return xmlDeclRe.ReplaceAll(data, []byte(`<?xml version="1.0"`))
}

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
	data = sanitizeXML(data)
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

// GenerateFeed builds an RSS feed document for the locally-downloaded episodes
// of a podcast. Only episodes with a downloaded file are included. The
// enclosure URLs are constructed using baseURL so that podcast clients can
// fetch the audio directly from this server.
//
// Episodes are sorted newest-first, matching standard podcast feed conventions.
func GenerateFeed(meta *PodcastMeta, episodes []EpisodeEntry, baseURL string) ([]byte, error) {
	// Collect only downloaded episodes, sorted newest-first.
	downloaded := make([]EpisodeEntry, 0, len(episodes))
	for _, ep := range episodes {
		if ep.Filename != "" {
			downloaded = append(downloaded, ep)
		}
	}
	slices.SortFunc(downloaded, func(a, b EpisodeEntry) int {
		return b.PubDate.Compare(a.PubDate)
	})

	items := make([]rssOutputItem, 0, len(downloaded))
	for _, ep := range downloaded {
		enclosureURL := baseURL + "/podcasts/" + meta.Slug + "/episodes/" + ep.Filename
		pubDate := ""
		if !ep.PubDate.IsZero() {
			pubDate = ep.PubDate.Format(time.RFC1123Z)
		}
		items = append(items, rssOutputItem{
			Title:       ep.Title,
			Description: ep.Description,
			GUID:        ep.GUID,
			PubDate:     pubDate,
			Enclosure: RSSEnclosure{
				URL:    enclosureURL,
				Length: strconv.FormatInt(ep.FileSize, 10),
				Type:   ep.EnclosureType,
			},
		})
	}

	coverURL := baseURL + "/podcasts/" + meta.Slug + "/cover.jpg"
	feed := &rssOutput{
		Version:  "2.0",
		ITunesNS: itunesNS,
		Channel: rssOutputChannel{
			Title:       meta.Title,
			Description: meta.Description,
			Image: &rssOutputImage{
				URL:   coverURL,
				Title: meta.Title,
				Link:  baseURL,
			},
			ITunesImage: &rssOutputITunesImage{Href: coverURL},
			Items:       items,
		},
	}

	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("generate feed: %w", err)
	}
	return append([]byte(xml.Header), out...), nil
}

// rssOutput, rssOutputChannel, and rssOutputItem are minimal RSS 2.0 structures
// used only for marshalling (output). The existing RSSFeed/RSSChannel/RSSItem
// types carry iTunes-namespace fields with xml tags that would emit empty
// elements when marshalled with zero values, so we use separate output types.
type rssOutput struct {
	XMLName  xml.Name         `xml:"rss"`
	Version  string           `xml:"version,attr"`
	ITunesNS string           `xml:"xmlns:itunes,attr"`
	Channel  rssOutputChannel `xml:"channel"`
}

// rssOutputImage is the standard RSS 2.0 <image> element.
type rssOutputImage struct {
	URL   string `xml:"url"`
	Title string `xml:"title"`
	Link  string `xml:"link,omitempty"`
}

// rssOutputITunesImage is the iTunes <itunes:image> element.
type rssOutputITunesImage struct {
	XMLName xml.Name `xml:"itunes:image"`
	Href    string   `xml:"href,attr"`
}

type rssOutputChannel struct {
	Title       string          `xml:"title"`
	Description string          `xml:"description,omitempty"`
	Image       *rssOutputImage `xml:"image,omitempty"`
	ITunesImage *rssOutputITunesImage
	Items       []rssOutputItem `xml:"item"`
}

type rssOutputItem struct {
	Title       string       `xml:"title"`
	Description string       `xml:"description,omitempty"`
	GUID        string       `xml:"guid"`
	PubDate     string       `xml:"pubDate,omitempty"`
	Enclosure   RSSEnclosure `xml:"enclosure"`
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

	// Build GUID set for O(1) lookups.
	knownGUIDs := make(map[string]struct{}, len(idx.Episodes))
	for _, ep := range idx.Episodes {
		knownGUIDs[ep.GUID] = struct{}{}
	}

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
		if _, exists := knownGUIDs[guid]; exists {
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
		knownGUIDs[guid] = struct{}{}
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

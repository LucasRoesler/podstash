package podstash

import (
	"encoding/xml"
	"errors"
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

// HTTPClient abstracts HTTP fetching for testability. Do is needed alongside
// Get so feed requests can carry conditional headers; *http.Client satisfies
// both.
type HTTPClient interface {
	Get(url string) (*http.Response, error)
	Do(req *http.Request) (*http.Response, error)
}

// ErrFeedNotModified reports that the server answered 304 Not Modified, so the
// feed is byte-identical to the one behind the validators we sent.
var ErrFeedNotModified = errors.New("feed not modified")

// FeedValidators are the HTTP cache validators a server gave us for a feed.
// Sending them back lets the server answer 304 instead of the whole document.
type FeedValidators struct {
	ETag         string
	LastModified string
}

// maxValidatorLen bounds a stored validator. Real ones are tens of bytes (an
// ETag is a hash, Last-Modified a fixed-width date), but response headers may
// be far larger, and these are persisted to disk and resent on every poll.
const maxValidatorLen = 512

// boundedValidator drops a validator too long to be genuine. Dropping one costs
// a full fetch on the next poll, which is what would happen without it anyway.
func boundedValidator(v string) string {
	if len(v) > maxValidatorLen {
		return ""
	}
	return v
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
	feed, _, err := FetchFeedConditional(client, url, FeedValidators{})
	return feed, err
}

// FetchFeedConditional downloads and parses a feed, sending any validators the
// server previously gave us. A server that recognises them answers 304 with an
// empty body, which skips the download and the XML parse entirely; that is
// returned as ErrFeedNotModified.
//
// The returned validators are the ones to send next time. A 304 response need
// not repeat them, so the ones passed in are echoed back when it does not.
func FetchFeedConditional(client HTTPClient, url string, prev FeedValidators) (*RSSFeed, FeedValidators, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, FeedValidators{}, fmt.Errorf("build feed request: %w", err)
	}
	if prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	if prev.LastModified != "" {
		req.Header.Set("If-Modified-Since", prev.LastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, FeedValidators{}, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	next := FeedValidators{
		ETag:         boundedValidator(resp.Header.Get("ETag")),
		LastModified: boundedValidator(resp.Header.Get("Last-Modified")),
	}

	if resp.StatusCode == http.StatusNotModified {
		// 304 bodies are empty and the response may omit the validators, so
		// keep the ones that produced the hit.
		if next.ETag == "" {
			next.ETag = prev.ETag
		}
		if next.LastModified == "" {
			next.LastModified = prev.LastModified
		}
		return nil, next, ErrFeedNotModified
	}

	if resp.StatusCode != http.StatusOK {
		return nil, FeedValidators{}, fmt.Errorf("fetch feed: status %d", resp.StatusCode)
	}

	const maxFeedSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize))
	if err != nil {
		return nil, FeedValidators{}, fmt.Errorf("read feed body: %w", err)
	}
	feed, err := ParseFeed(data)
	if err != nil {
		return nil, FeedValidators{}, fmt.Errorf("parse feed: %w", err)
	}
	return feed, next, nil
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

// ForceRefreshPodcast refreshes ignoring stored cache validators, so the feed
// is fetched and parsed in full. Used for refreshes a person explicitly asked
// for, where a silent 304 would make the action look broken.
func ForceRefreshPodcast(client HTTPClient, dataDir string, slug string) (int, error) {
	return refreshPodcast(client, dataDir, slug, true)
}

// RefreshPodcast fetches the RSS feed for a podcast and adds any new episodes
// to the index. Returns the number of new episodes added.
//
// The fetch is conditional: when a usable index and stored validators exist,
// a server that answers 304 lets this skip the download, the parse, and the
// index write. Use ForceRefreshPodcast to bypass that.
func RefreshPodcast(client HTTPClient, dataDir string, slug string) (int, error) {
	return refreshPodcast(client, dataDir, slug, false)
}

func refreshPodcast(client HTTPClient, dataDir string, slug string, force bool) (int, error) {
	dir := PodcastDir(dataDir, slug)
	mu := podcastMu(slug)
	mu.Lock()
	defer mu.Unlock()

	meta, err := LoadMeta(dir)
	if err != nil {
		return 0, fmt.Errorf("refresh %s: %w", slug, err)
	}

	// Load the index before fetching, because whether it already holds episodes
	// decides if the conditional fast path is safe.
	idx, err := LoadIndex(dir)
	if err != nil {
		return 0, fmt.Errorf("refresh %s: %w", slug, err)
	}

	// Send validators only when there is an index worth preserving. A 304 says
	// the feed is unchanged relative to what we stored, which is worthless when
	// nothing was stored: the fast path would skip the rebuild and the same
	// ETag would return 304 on every future poll, stranding the podcast with an
	// empty index. An empty index is reachable without corruption, since a
	// podcast is created with one (handlers.go) and SaveMeta persists the ETag
	// before SaveIndex runs, so a failed index write leaves exactly that state.
	prev := FeedValidators{}
	if !force && len(idx.Episodes) > 0 {
		prev = FeedValidators{ETag: meta.ETag, LastModified: meta.LastModified}
	}

	feed, validators, err := FetchFeedConditional(client, meta.FeedURL, prev)
	if errors.Is(err, ErrFeedNotModified) {
		// The feed is unchanged, so the index is left alone and the poll time
		// is not recorded here: writing meta on every poll is what kept
		// spinning disks awake (issue #8), and PollHeartbeat holds it instead.
		//
		// Validators are the exception. A 304 can carry ones we do not hold,
		// most importantly a server that has gained a strong ETag for a feed
		// we only track by Last-Modified. Nothing else would ever store it,
		// since this branch is the only one reached while the feed is quiet,
		// so the conditional request would stay permanently weaker. Saving
		// only on a difference keeps the quiet path write-free.
		if validators.ETag != meta.ETag || validators.LastModified != meta.LastModified {
			meta.ETag = validators.ETag
			meta.LastModified = validators.LastModified
			if err := SaveMeta(dir, meta); err != nil {
				return 0, fmt.Errorf("refresh %s: %w", slug, err)
			}
		}
		return 0, nil
	}
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
	meta.ETag = validators.ETag
	meta.LastModified = validators.LastModified

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

	if added > 0 {
		meta.LastChangedAt = time.Now().UTC()
	}

	// Both saves skip the write when the bytes are unchanged, so a 200 that
	// turns out to carry nothing new still touches no disk.
	if err := SaveMeta(dir, meta); err != nil {
		return added, fmt.Errorf("refresh %s: %w", slug, err)
	}
	if err := SaveIndex(dir, idx); err != nil {
		return added, fmt.Errorf("refresh %s: %w", slug, err)
	}

	return added, nil
}

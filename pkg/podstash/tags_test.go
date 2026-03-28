package podstash

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogem/id3v2/v2"
)

func copyTestMP3(t *testing.T, dir string) string {
	t.Helper()
	src, err := os.ReadFile("testdata/episode.mp3")
	if err != nil {
		t.Fatalf("read test mp3: %v", err)
	}
	dst := filepath.Join(dir, "test.mp3")
	if err := os.WriteFile(dst, src, 0644); err != nil {
		t.Fatalf("write test mp3: %v", err)
	}
	return dst
}

func TestWriteID3Tags(t *testing.T) {
	dir := t.TempDir()
	mp3Path := copyTestMP3(t, dir)

	meta := &PodcastMeta{
		Title:  "My Podcast",
		Author: "Test Author",
	}
	ep := &EpisodeEntry{
		Title:       "Episode One",
		PubDate:     time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
		Description: "A great episode about testing.",
	}

	err := TagMP3(mp3Path, meta, ep, "")
	if err != nil {
		t.Fatalf("TagMP3: %v", err)
	}

	// Read back and verify.
	tag, err := id3v2.Open(mp3Path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatalf("open tagged file: %v", err)
	}
	defer tag.Close()

	if tag.Title() != "Episode One" {
		t.Errorf("Title = %q", tag.Title())
	}
	if tag.Album() != "My Podcast" {
		t.Errorf("Album = %q", tag.Album())
	}
	if tag.Artist() != "Test Author" {
		t.Errorf("Artist = %q", tag.Artist())
	}
	if tag.Genre() != "Podcast" {
		t.Errorf("Genre = %q", tag.Genre())
	}
	if tag.Year() != "2025" {
		t.Errorf("Year = %q", tag.Year())
	}

	// Check comment.
	comments := tag.GetFrames(tag.CommonID("Comments"))
	if len(comments) == 0 {
		t.Error("expected comment frame")
	} else {
		cf, ok := comments[0].(id3v2.CommentFrame)
		if !ok {
			t.Error("comment frame is wrong type")
		} else if cf.Text != "A great episode about testing." {
			t.Errorf("Comment = %q", cf.Text)
		}
	}
}

func TestWriteID3TagsWithCover(t *testing.T) {
	dir := t.TempDir()
	mp3Path := copyTestMP3(t, dir)

	// Create a fake cover image.
	coverPath := filepath.Join(dir, "cover.jpg")
	coverData := []byte("fake jpeg data for testing")
	os.WriteFile(coverPath, coverData, 0644)

	meta := &PodcastMeta{Title: "Cover Test", Author: "Author"}
	ep := &EpisodeEntry{Title: "With Cover"}

	err := TagMP3(mp3Path, meta, ep, coverPath)
	if err != nil {
		t.Fatalf("TagMP3: %v", err)
	}

	tag, err := id3v2.Open(mp3Path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer tag.Close()

	pics := tag.GetFrames(tag.CommonID("Attached picture"))
	if len(pics) == 0 {
		t.Fatal("expected attached picture")
	}
	pf, ok := pics[0].(id3v2.PictureFrame)
	if !ok {
		t.Fatal("picture frame is wrong type")
	}
	if string(pf.Picture) != string(coverData) {
		t.Errorf("cover data mismatch: got %d bytes", len(pf.Picture))
	}
	if pf.PictureType != id3v2.PTFrontCover {
		t.Errorf("PictureType = %d, want FrontCover", pf.PictureType)
	}
}

func TestSkipNonMP3(t *testing.T) {
	dir := t.TempDir()
	m4aPath := filepath.Join(dir, "episode.m4a")
	os.WriteFile(m4aPath, []byte("not an mp3"), 0644)

	meta := &PodcastMeta{Title: "Test"}
	ep := &EpisodeEntry{Title: "Episode"}

	err := TagMP3(m4aPath, meta, ep, "")
	if err != nil {
		t.Errorf("expected no error for non-MP3, got: %v", err)
	}

	// Verify file was not modified.
	data, _ := os.ReadFile(m4aPath)
	if string(data) != "not an mp3" {
		t.Error("non-MP3 file should not be modified")
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bogem/id3v2/v2"
)

// TagMP3 writes ID3v2.4 tags to a downloaded MP3 file.
// It embeds podcast and episode metadata so the file is self-describing.
func TagMP3(filePath string, meta *PodcastMeta, ep *EpisodeEntry, coverPath string) error {
	if !isMP3(filePath) {
		return nil
	}

	tag, err := id3v2.Open(filePath, id3v2.Options{Parse: false})
	if err != nil {
		return err
	}
	defer tag.Close()

	tag.SetDefaultEncoding(id3v2.EncodingUTF8)
	tag.SetVersion(4)

	tag.SetTitle(ep.Title)
	tag.SetAlbum(meta.Title)
	tag.SetArtist(meta.Author)
	tag.SetGenre("Podcast")

	if !ep.PubDate.IsZero() {
		tag.SetYear(ep.PubDate.Format("2006"))
	}

	// Use full description (not truncated) as comment.
	if ep.Description != "" {
		comment := id3v2.CommentFrame{
			Encoding:    id3v2.EncodingUTF8,
			Language:    "eng",
			Description: "",
			Text:        ep.Description,
		}
		tag.AddCommentFrame(comment)
	}

	// Embed cover art if available.
	if coverPath != "" {
		coverData, err := os.ReadFile(coverPath)
		if err == nil && len(coverData) > 0 {
			pic := id3v2.PictureFrame{
				Encoding:    id3v2.EncodingUTF8,
				MimeType:    "image/jpeg",
				PictureType: id3v2.PTFrontCover,
				Description: "Cover",
				Picture:     coverData,
			}
			tag.AddAttachedPicture(pic)
		}
	}

	return tag.Save()
}

// isMP3 checks if the file has an .mp3 extension.
func isMP3(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".mp3")
}

package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/vincommerce/backend/internal/domain"
)

// Allowed raster image types (magic bytes -> extension).
// SVG is deliberately rejected: it can carry active content and would be a
// stored-XSS vector when served same-origin.
var imageSignatures = map[string][]string{
	"image/jpeg": {"\xff\xd8\xff"},
	"image/png":  {"\x89PNG\r\n\x1a\n"},
	"image/gif":  {"GIF87a", "GIF89a"},
}

// MediaService stores uploaded files on disk.
type MediaService struct {
	dir      string
	baseURL  string
	maxBytes int64
}

// NewMediaService creates a MediaService.
func NewMediaService(dir, baseURL string, maxBytes int64) *MediaService {
	return &MediaService{dir: dir, baseURL: baseURL, maxBytes: maxBytes}
}

// Save stores an image and returns its public URL.
func (s *MediaService) Save(file multipart.File, header *multipart.FileHeader) (string, error) {
	if header.Size > s.maxBytes {
		return "", domain.E(domain.KindInvalid, "FILE_TOO_LARGE",
			fmt.Sprintf("file exceeds %d MB limit", s.maxBytes/1024/1024))
	}

	head := make([]byte, 12)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", domain.E(domain.KindInvalid, "FILE_READ_FAILED", "unable to read file")
	}
	head = head[:n]

	contentType := detectContentType(head)
	if contentType == "" {
		return "", domain.E(domain.KindInvalid, "UNSUPPORTED_TYPE", "only jpeg, png, webp and gif images are allowed")
	}

	ext := extensionFor(contentType)
	name := randomName() + ext
	path := filepath.Join(s.dir, name)
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", domain.Wrap(domain.KindInternal, "UPLOAD_FAILED", "unable to create upload dir", err)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", domain.Wrap(domain.KindInternal, "UPLOAD_FAILED", "unable to rewind file", err)
	}
	dst, err := os.Create(path)
	if err != nil {
		return "", domain.Wrap(domain.KindInternal, "UPLOAD_FAILED", "unable to write file", err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		return "", domain.Wrap(domain.KindInternal, "UPLOAD_FAILED", "write failed", err)
	}

	return s.baseURL + "/uploads/" + name, nil
}

func detectContentType(head []byte) string {
	for ct, sigs := range imageSignatures {
		for _, sig := range sigs {
			if bytes.HasPrefix(head, []byte(sig)) {
				return ct
			}
		}
	}
	// WebP: "RIFF" at offset 0 and "WEBP" at offset 8 (RIFF alone also matches
	// WAV/AVI, so both markers must be present).
	if len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

func extensionFor(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".bin"
}

func randomName() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

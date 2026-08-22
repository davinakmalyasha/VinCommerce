package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/vincommerce/backend/internal/domain"
)

// Allowed image types (magic bytes -> extension).
var imageSignatures = map[string][]string{
	"image/jpeg":    {"\xff\xd8\xff"},
	"image/png":     {"\x89PNG\r\n\x1a\n"},
	"image/webp":    {"RIFF", "WEBP"},
	"image/gif":     {"GIF87a", "GIF89a"},
	"image/svg+xml": {"<svg"},
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

	contentType := detectContentType(head, header.Filename)
	if contentType == "" {
		return "", domain.E(domain.KindInvalid, "UNSUPPORTED_TYPE", "only jpeg, png, webp, gif and svg images are allowed")
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

func detectContentType(head []byte, filename string) string {
	for ct, sigs := range imageSignatures {
		for _, sig := range sigs {
			if len(head) >= len(sig) && strings.HasPrefix(string(head), sig) {
				return ct
			}
		}
	}
	if strings.HasSuffix(strings.ToLower(filename), ".svg") {
		return "image/svg+xml"
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
	case "image/svg+xml":
		return ".svg"
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

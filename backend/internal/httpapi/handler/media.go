package handler

import (
	"net/http"
	"strings"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/service"
)

// Media exposes file uploads (auth).
type Media struct {
	svc *service.MediaService
}

// NewMedia creates a Media handler.
func NewMedia(svc *service.MediaService) *Media {
	return &Media{svc: svc}
}

// MaxUploadBytes is the hard ceiling on an upload body, including multipart
// framing overhead.
const MaxUploadBytes int64 = 6 << 20 // 6 MiB

// Upload handles POST /media/upload (multipart field "file").
func (h *Media) Upload(w http.ResponseWriter, r *http.Request) {
	// Cap the BODY before parsing. ParseMultipartForm spools each part to a
	// temp file, and the service's size check inspects header.Size only
	// afterwards — so without a reader-level limit the declared 5 MB cap was
	// decorative and an unauthenticated-ish caller could fill the disk. This
	// must be set before ParseMultipartForm.
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if strings.Contains(err.Error(), "too large") {
			writeErr(w, r, domain.E(domain.KindInvalid, "FILE_TOO_LARGE",
				"upload exceeds the maximum size"))
			return
		}
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_MULTIPART", "unable to parse upload"))
		return
	}
	// Anything ParseMultipartForm spooled to disk must be released; otherwise
	// the temp files accumulate for the process lifetime.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "FILE_REQUIRED", "file field is required"))
		return
	}
	defer file.Close()

	url, err := h.svc.Save(file, header)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": url})
}

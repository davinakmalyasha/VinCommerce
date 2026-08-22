package handler

import (
	"net/http"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
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

// Upload handles POST /media/upload (multipart field "file").
func (h *Media) Upload(w http.ResponseWriter, r *http.Request) {
	middleware.UserFrom(r.Context())

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_MULTIPART", "unable to parse upload"))
		return
	}
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

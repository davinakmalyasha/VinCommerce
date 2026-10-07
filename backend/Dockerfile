# syntax=docker/dockerfile:1
# Base images are pinned by digest, not by tag: `golang:1.26-alpine` and
# `alpine:3.21` move as soon as a patch lands, so a rebuild weeks from now
# silently changes the toolchain or libc a previously-green build used.
# .github/dependabot.yml tracks the digests and opens the bump PR.
FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Inject the git revision so /health reports what's actually deployed.
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${GIT_SHA}" -o /out/api ./cmd/api

FROM alpine:3.21@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507
RUN adduser -D -u 10001 app
# Create the upload directory in the image, owned by the runtime user, BEFORE
# dropping privileges.
#
# The compose file mounts a named volume (`uploads:/data/uploads`) over this
# path. A fresh named volume is created root-owned and 0755, so the uid 10001
# process could not create files inside it: every single upload failed with
# EACCES. `service/media.go` does call `os.MkdirAll(s.dir, 0o755)`, but that
# only helps when the parent is writable, and here it is not.
#
# Chowning the mountpoint in the image is the fix for the case where the volume
# is a bind mount of an existing directory; for a fresh named volume Docker
# copies the image's directory content and ownership into it on first use, which
# is why this has to happen in the image rather than in an entrypoint script.
RUN mkdir -p /data/uploads && chown -R app:app /data
USER app
COPY --from=builder /out/api /usr/local/bin/api
EXPOSE 8080
# Readiness (not liveness): fails when Postgres/Redis are unreachable so the
# container is actually marked unhealthy instead of always reporting 200.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/v1/health/ready || exit 1
ENTRYPOINT ["api"]

# syntax=docker/dockerfile:1
# Base images are pinned by digest, not by tag: `golang:1.26-alpine` and
# `alpine:3.21` move as soon as a patch lands, so a rebuild weeks from now
# silently changes the toolchain or libc a previously-green build used.
# .github/dependabot.yml tracks the digests and opens the bump PR.
FROM golang:1.26-alpine@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Inject the git revision so /health reports what's actually deployed.
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${GIT_SHA}" -o /out/api ./cmd/api

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN adduser -D -u 10001 app
USER app
COPY --from=builder /out/api /usr/local/bin/api
EXPOSE 8080
# Readiness (not liveness): fails when Postgres/Redis are unreachable so the
# container is actually marked unhealthy instead of always reporting 200.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/v1/health/ready || exit 1
ENTRYPOINT ["api"]

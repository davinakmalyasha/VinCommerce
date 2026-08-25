# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Inject the git revision so /health reports what's actually deployed.
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${GIT_SHA}" -o /out/api ./cmd/api

FROM alpine:3.21
RUN adduser -D -u 10001 app
USER app
COPY --from=builder /out/api /usr/local/bin/api
EXPOSE 8080
# Readiness (not liveness): fails when Postgres/Redis are unreachable so the
# container is actually marked unhealthy instead of always reporting 200.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/v1/health/ready || exit 1
ENTRYPOINT ["api"]

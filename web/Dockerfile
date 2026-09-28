# syntax=docker/dockerfile:1
# Pinned by digest — see the comment in backend/Dockerfile for why.
FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS builder
WORKDIR /src
COPY web/package.json web/package-lock.json ./
# --prefer-offline: a warm npm cache makes this a no-op re-validate instead of a
# full re-download, without ever silently using a lockfile-inconsistent version.
RUN npm ci --prefer-offline
COPY web/ ./

# Vite inlines import.meta.env.VITE_* into the bundle at BUILD time, so these
# are the only way the documented VITE_MIDTRANS_CLIENT_KEY can ever reach a
# container image. Without the ARGs below, `docker build` produced a bundle with
# the Midtrans button permanently disabled no matter what the operator set in
# the environment. Vite only exposes VITE_-prefixed names, and a client-side
# key is public by design — never add a VITE_-prefixed name that holds a secret.
ARG VITE_MIDTRANS_CLIENT_KEY
ARG VITE_MIDTRANS_SNAP_URL
ENV VITE_MIDTRANS_CLIENT_KEY=${VITE_MIDTRANS_CLIENT_KEY}
ENV VITE_MIDTRANS_SNAP_URL=${VITE_MIDTRANS_SNAP_URL}
RUN npm run build

FROM nginx:1.29-alpine@sha256:5616878291a2eed594aee8db4dade5878cf7edcb475e59193904b198d9b830de AS runtime

# The final stage ran as root: nginx master bound :80 (a privileged port, which
# is *why* it was root) and then spawned workers as `nginx`, but the container's
# entire attack surface was a root process. Unprivileged ports (>=1024) exist, so
# the whole container now runs as `nginx` and nothing inside it is root.
#
# nginx needs to write outside the served tree: the master writes the pid file
# and opens the listening sockets, and both master and worker write proxied
# request/response buffers and temp bodies to disk. Create those directories
# explicitly instead of relying on whatever the base image happens to ship —
# the `|| true` guards make this a no-op on a base image that already provides
# the nginx user or the directories.
RUN set -eux; \
    addgroup -g 101 -S nginx 2>/dev/null || true; \
    adduser -S -D -H -u 101 -h /var/cache/nginx -s /sbin/nologin -G nginx -g nginx nginx 2>/dev/null || true; \
    mkdir -p \
      /etc/nginx/snippets \
      /var/cache/nginx/client_temp \
      /var/cache/nginx/proxy_temp \
      /var/cache/nginx/fastcgi_temp \
      /var/cache/nginx/uwsgi_temp \
      /var/cache/nginx/scgi_temp \
      /var/log/nginx \
      /var/run; \
    chown -R nginx:nginx \
      /var/cache/nginx \
      /var/log/nginx \
      /var/run \
      /usr/share/nginx/html; \
    # The packaged nginx.conf carries `user nginx;`, which is only meaningful
    # when the master runs with super-user privileges. Left in place it makes
    # nginx log "the 'user' directive makes sense only if the master process
    # runs with super-user privileges, ignored" on every start. Removing the
    # directive is the honest form: we are already the user it named.
    sed -i -e '/^user[[:space:]]/d' /etc/nginx/nginx.conf

COPY --from=builder /src/dist /usr/share/nginx/html
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
# Included by nginx.conf from every location block. Must exist or nginx refuses
# to start — see the comment at the top of that file.
COPY web/security-headers.conf /etc/nginx/snippets/security-headers.conf
COPY web/nginx-brotli.conf /etc/nginx/snippets/brotli.conf

# The base image's ENTRYPOINT runs /docker-entrypoint.d/*.sh BEFORE nginx, and
# 10-listen-on-ipv6.sh rewrites conf.d/default.conf in place. COPY leaves that
# file root-owned, so under `USER nginx` the entrypoint's write fails and the
# container dies on start with a permission error that looks nothing like a
# config problem. Hand it to nginx as well.
RUN chown nginx:nginx /etc/nginx/conf.d/default.conf /etc/nginx/snippets/*.conf

# ngx_brotli is NOT compiled into the official nginx images (they ship only
# xslt/geoip/image-filter/njs/acme) and nginx.org publishes no brotli package,
# so a bare `brotli on;` in nginx.conf makes `nginx -t` fail with
# "unknown directive" and the container never starts. Enable the snippet only
# when the module is actually present, so `nginx -t` always passes and brotli
# switches itself on the day the base image (or a custom image) gains the
# module. Until then gzip carries the compression.
RUN if nginx -V 2>&1 | grep -q -- '--with-brotli'; then \
      echo "ngx_brotli present: brotli compression enabled"; \
    else \
      : > /etc/nginx/snippets/brotli.conf; \
      echo "ngx_brotli absent: brotli snippet emptied, gzip only"; \
    fi

# Config syntax gate at BUILD time, so a typo in nginx.conf or a missing
# snippet fails here instead of after a full build+push. `nginx -t` resolves
# every proxy_pass upstream hostname at config-parse time, and `api` does not
# exist in the build sandbox, so the upstream is swapped to a literal address
# for the duration of the check and the real config is restored afterwards.
RUN set -eux; \
    cp /etc/nginx/conf.d/default.conf /tmp/default.conf.real; \
    sed 's|http://api:8080|http://127.0.0.1:8080|g' /tmp/default.conf.real \
      > /etc/nginx/conf.d/default.conf; \
    nginx -t; \
    cp /tmp/default.conf.real /etc/nginx/conf.d/default.conf; \
    rm -f /tmp/default.conf.real

USER nginx
# 8080, not 80: binding a privileged port is the only reason this stage needed
# root, and an unprivileged port lets the whole container drop to `nginx`.
# Callers must publish `8080:8080` (see infra/compose/compose.full.yaml).
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1
CMD ["nginx", "-g", "daemon off;"]

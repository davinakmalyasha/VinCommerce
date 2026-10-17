#!/usr/bin/env bash
# RUNS THE BUILT WEB IMAGE and asserts three things: it becomes healthy, it serves
# the SPA, and the security headers reach the HTML document.
#
# Used by both the full CI pipeline and the manual web-image-probe workflow, so the
# two cannot drift. Docker is only ever invoked on a CI runner; this script is never
# run locally, and nothing in the repo requires Docker on a developer machine.
#
#   usage: bash scripts/probe-web-image.sh <image-ref>
#
# ON FAILURE IT PRINTS A DOSSIER. "unhealthy" is not an answer on its own: the first
# version of this check printed only `docker logs`, which showed the entrypoint
# completing with no nginx error -- not enough to tell a container that never
# started from one that started and cannot serve. `.State.Health` records every
# probe with its exit code and captured output, which is what separates "connection
# refused" from "404" from "wget: not found".
set -uo pipefail

IMAGE="${1:?usage: probe-web-image.sh <image-ref>}"

# IMAGE INSPECTION FIRST, because it does not depend on the container staying up.
#
# The failure this was written for was nginx exiting in 0.7s with
# `open() "/var/cache/nginx/nginx.pid" failed (13: Permission denied)` -- after the
# Dockerfile had already `chown -R`'d that directory. Asking the IMAGE what the
# ownership actually is answers that in seconds; asking the RUNNING container is
# impossible once it has exited.
echo "===== image inspection ====="
echo "runtime identity:"
docker run --rm "$IMAGE" id 2>&1 || true
echo "ownership and mode (as root, so nothing is hidden by permissions):"
docker run --rm --user root "$IMAGE" sh -c '
  stat -c "%u:%g %a %n" /var/cache/nginx /run /tmp /dev/shm /var/lib 2>&1
' || true
echo "can the runtime user actually write the pid directory?"
docker run --rm --user 101:101 "$IMAGE" sh -c '
  if touch /var/cache/nginx/.write-probe 2>/dev/null; then
    echo "WRITABLE: /var/cache/nginx"
  else
    echo "NOT WRITABLE: /var/cache/nginx"
  fi
' 2>&1 || true
echo "is /var/cache/nginx a volume?"
docker inspect -f '{{json .Mounts}}' "$IMAGE" 2>&1 || true
echo "===== starting the container ====="

docker run -d --name vc-web-probe -p 8080:8080 --add-host api:127.0.0.1 "$IMAGE"

dump() {
  echo "===== container state =====" >&2
  docker inspect -f '{{json .State}}' vc-web-probe 2>&1 || true
  echo "===== health probes (each run, with output) =====" >&2
  docker inspect -f '{{json .State.Health}}' vc-web-probe 2>&1 || true
  echo "===== docker logs =====" >&2
  docker logs vc-web-probe 2>&1 || true
  echo "===== inside the container =====" >&2
  docker exec vc-web-probe sh -c '
    echo "pid1: $(cat /proc/1/comm 2>/dev/null)"
    ps aux 2>/dev/null || ps 2>/dev/null || true
    echo "--- listening ---"
    (ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null) || true
    echo "--- permissions ---"
    ls -ld /var/cache/nginx /run /tmp /usr/share/nginx/html 2>&1
    echo "--- main config pid/user ---"
    grep -nE "^(pid|user)" /etc/nginx/nginx.conf 2>&1 || true
    echo "--- listen directives ---"
    nginx -T 2>&1 | grep -n "listen" | head -10
    echo "--- manual probes ---"
    wget -S -O /dev/null http://127.0.0.1:8080/ 2>&1 | head -20
    curl -sS -o /dev/null -w "curl http_code=%{http_code}" http://127.0.0.1:8080/ 2>&1 || true
  ' 2>&1 || echo "(container is not running: docker exec failed)"
}

cleanup() { docker rm -f vc-web-probe >/dev/null 2>&1 || true; }
trap cleanup EXIT

# 120s, not 60: the image's HEALTHCHECK is
# `--start-period=5s --interval=30s --retries=3`, so Docker does not call the
# container unhealthy until roughly 65s. A 60s window could give up before Docker
# had finished deciding, and report a timeout as if it were the container's fault.
for i in $(seq 1 60); do
  status=$(docker inspect -f '{{.State.Health.Status}}' vc-web-probe 2>/dev/null || echo starting)
  echo "probe ${i}: ${status}"
  if [ "$status" = "healthy" ]; then
    echo "container reported healthy"
    curl -fsS -o /dev/null -w 'GET / -> %{http_code}' http://127.0.0.1:8080/
    echo
    # The security headers were once dropped from the HTML document AND from every
    # hashed asset, because each of those locations defined its own `add_header` --
    # and nginx's `add_header` is NOT inherited into a block that defines one.
    curl -fsSI http://127.0.0.1:8080/ | grep -i '^content-security-policy:'
    curl -fsSI http://127.0.0.1:8080/ | grep -i '^strict-transport-security:'
    curl -fsSI http://127.0.0.1:8080/ | grep -i '^x-content-type-options:'
    exit 0
  fi
  if [ "$status" = "unhealthy" ]; then
    echo "container reported unhealthy" >&2
    dump
    exit 1
  fi
  sleep 2
done

echo "container never became healthy within 120s" >&2
dump
exit 1

#!/usr/bin/env bash
# End-to-end tests: the plugin running inside Traefik (Yaegi), with Redis and the
# sample backends of compose.yml.
#
#   docker compose up -d --wait ingress cache whoami placeholder
#   ./e2e.sh
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
RUN="$(date +%s)$RANDOM" # unique URLs: every run starts from a miss
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
failures=0

# request <name> <curl args…>: saves the response headers and body under $TMP/<name>.
request() {
  local name="$1"
  shift
  curl -sS --max-time 10 -o "$TMP/$name.body" -D "$TMP/$name.headers" "$@"
}

header() { # header <name> <header>: last value of a response header
  grep -i "^$2:" "$TMP/$1.headers" | tail -n 1 | cut -d ' ' -f 2- | tr -d '\r'
}

status() { # status <name>: final status code
  grep '^HTTP/' "$TMP/$1.headers" | tail -n 1 | cut -d ' ' -f 2
}

check() { # check <description> <actual> <expected substring>
  if [[ "$2" == *"$3"* ]]; then
    echo "ok    $1"
  else
    echo "FAIL  $1: got '$2', want '$3'"
    failures=$((failures + 1))
  fi
}

echo "# Waiting for $BASE_URL"
for _ in $(seq 1 60); do
  if curl -sf --max-time 5 -o /dev/null "$BASE_URL/whoami?ready=$RUN" &&
    curl -sf --max-time 5 -o /dev/null "$BASE_URL/600x600?ready=$RUN"; then
    break
  fi
  sleep 1
done

echo "# Miss, then hit (explicit freshness: placeholder sends max-age)"
url="$BASE_URL/600x600?b=2&a=1&run=$RUN"
request miss "$url"
check "first request is a miss" "$(header miss Cache-Status)" "traedis; fwd=uri-miss; fwd-status=200"
request hit "$url"
check "second request is a hit" "$(header hit Cache-Status)" "traedis; hit; ttl="
check "hit status" "$(status hit)" "200"
if [[ "$(header hit Age)" =~ ^[0-9]+$ ]]; then
  echo "ok    hit has Age"
else
  echo "FAIL  hit has no valid Age: '$(header hit Age)'"
  failures=$((failures + 1))
fi
check "hit has the same Content-Type" "$(header hit Content-Type)" "$(header miss Content-Type)"
if cmp -s "$TMP/miss.body" "$TMP/hit.body"; then
  echo "ok    hit body is identical"
else
  echo "FAIL  hit body differs from the miss body"
  failures=$((failures + 1))
fi

echo "# Key normalization and HEAD"
request sorted "$BASE_URL/600x600?a=1&b=2&run=$RUN"
check "query parameter order is ignored" "$(header sorted Cache-Status)" "traedis; hit"
request head -I "$url"
check "HEAD is served from the stored GET" "$(header head Cache-Status)" "traedis; hit"
check "HEAD has Content-Length" "$(header head Content-Length)" "$(wc -c <"$TMP/miss.body" | tr -d ' ')"

echo "# defaultTtl (whoami sends no Cache-Control)"
url="$BASE_URL/whoami?run=$RUN"
request whoami1 "$url"
check "first request is a miss" "$(header whoami1 Cache-Status)" "traedis; fwd=uri-miss; fwd-status=200"
request whoami2 "$url"
check "second request is a hit" "$(header whoami2 Cache-Status)" "traedis; hit; ttl="

echo "# Request directives and bypasses"
request nocache -H "Cache-Control: no-cache" "$url"
check "request no-cache is forwarded" "$(header nocache Cache-Status)" "traedis; fwd=request; fwd-status=200"
request post -X POST "$url"
check "POST is not handled" "$(header post Cache-Status)" "traedis; fwd=method"
request sse -H "Accept: text/event-stream" "$url"
check "event streams are not handled" "$(header sse Cache-Status)" "traedis; fwd=bypass"

echo "# Nothing personal is shared"
url="$BASE_URL/whoami?auth=$RUN"
request auth1 -H "Authorization: Bearer secret" "$url"
request auth2 -H "Authorization: Bearer secret" "$url"
check "Authorization without explicit freshness is not stored" "$(header auth2 Cache-Status)" "traedis; fwd=uri-miss"
url="$BASE_URL/whoami?cookie=$RUN"
request cookie1 -H "Cookie: sid=1" "$url"
request cookie2 -H "Cookie: sid=1" "$url"
check "Cookie without explicit freshness is not stored" "$(header cookie2 Cache-Status)" "traedis; fwd=uri-miss"

echo "# Fail open when Redis is down"
docker compose stop cache >/dev/null 2>&1
request down "$BASE_URL/600x600?b=2&a=1&run=$RUN"
check "backend still answers" "$(status down)" "200"
check "cache is bypassed" "$(header down Cache-Status)" "traedis; fwd=bypass; fwd-status=200; detail=redis"
docker compose up -d --wait cache >/dev/null 2>&1

echo "# Cache works again once Redis is back"
url="$BASE_URL/whoami?back=$RUN"
for _ in $(seq 1 10); do
  request back "$url"
  if [[ "$(header back Cache-Status)" == *"hit"* ]]; then
    break
  fi
  sleep 1
done
check "hit after Redis restart" "$(header back Cache-Status)" "traedis; hit"

if [[ "$failures" -gt 0 ]]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "All checks passed"

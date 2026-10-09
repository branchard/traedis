> [!WARNING]
> **This project is a work in progress.** Use it at your own risk.

<!--suppress HtmlDeprecatedAttribute -->
<p align="center">
  <img src="assets/icon.svg" alt="Traedis Logo" height="360">
</p>

# Traedis

[![CI](https://github.com/branchard/traedis/actions/workflows/ci.yml/badge.svg)][ci]

**Redis**-backed HTTP cache for **Traefik**, shipped as a **middleware plugin**.

- **Standards-based**: follows [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111) (`Cache-Control`, `ETag`, `Last-Modified`,
  `304 Not Modified`…), [RFC 5861](https://www.rfc-editor.org/rfc/rfc5861) (`stale-while-revalidate`, `stale-if-error`) and
  [RFC 9211](https://www.rfc-editor.org/rfc/rfc9211) (`Cache-Status`).
- **Safe by default**: never caches what must not be shared (`Authorization`, `Set-Cookie`, `private`) unless the backend explicitly allows
  it. See [Security](#security) for the pitfalls.
- **Fail open**: if Redis is slow or down, requests go straight to the backend.
- **Observable**: every response carries a `Cache-Status` header (`hit`, `fwd=uri-miss`, `fwd=bypass`…).
- **Stream-friendly**: large bodies, WebSockets and server-sent events pass through untouched.
- **Persistent and shared**: responses are stored in Redis, not in Traefik's memory, so the cache survives Traefik restarts and redeploys,
  and all Traefik replicas share it.

## Requirements

- Traefik v3
- Redis ≥ 8.0 (uses `HSETEX`)

## Installation

Declare the plugin in the Traefik **static** configuration:

```yaml
experimental:
  plugins:
    traedis:
      moduleName: github.com/branchard/traedis
      version: vX.Y.Z # replace with the release you want
```

Then use it as a middleware in the **dynamic** configuration:

```yaml
http:
  routers:
    my-app:
      rule: Host(`app.example.com`)
      middlewares:
        - ratelimit # before the cache: random URLs are misses that reach the backend
        - cache
      service: my-app

  middlewares:
    ratelimit:
      rateLimit:
        average: 50
        burst: 100

    cache:
      plugin:
        traedis:
          redis:
            dsn: "redis://:password@redis:6379/0"
          defaultTtl: 10m

  services:
    my-app:
      loadBalancer:
        servers:
          - url: "http://my-app:3000"
```

A complete local setup (Traefik, Redis, sample backends) is available in [`compose.yml`](compose.yml) and [`example/`](example).

## Middleware Configuration Keys

All keys are optional.

| Key                           | Default                | Description                                                                                                                                                |
|-------------------------------|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `redis.dsn`                   | `redis://cache:6379/0` | Redis connection string: `redis://[user[:password]@]host[:port][/db]`. TLS (`rediss://`) is not supported yet.                                             |
| `redis.timeout`               | `50ms`                 | Maximum time for a Redis operation. Past it, the cache is bypassed.                                                                                        |
| `statusCodes`                 | `[200]`                | Status codes allowed to be stored. It only narrows what RFC 9111 allows; it never makes a response cacheable.                                              |
| `defaultTtl`                  | `5m`                   | Freshness when the backend sends no `max-age`, `s-maxage` or `Expires`. `0` disables it.                                                                   |
| `staleTtl`                    | `1h`                   | How long an entry is kept in Redis after it expires, hence the upper bound of `stale-while-revalidate` and `stale-if-error`. `0` disables stale responses. |
| `defaultStaleWhileRevalidate` | `0s`                   | `stale-while-revalidate` window for responses that don't send the directive. `0` disables it. See [Stale responses](#stale-responses).                     |
| `defaultStaleIfError`         | `0s`                   | `stale-if-error` window for responses that don't send the directive. `0` disables it.                                                                      |
| `maxBodyBytes`                | `5242880` (5 MiB)      | Larger responses are streamed to the client and not cached.                                                                                                |
| `maxVariants`                 | `16`                   | Stored variants per URL, for responses with `Vary`. `0`: responses with `Vary` are not cached. See [Variants](#variants-vary).                             |
| `sortQuery`                   | `false`                | Sort the query parameters by name in the cache key: `?a=1&b=2` and `?b=2&a=1` share the same entry. Only for backends that ignore their order.             |
| `exposeKey`                   | `false`                | Add the cache key to the `Cache-Status` header (`key="…"`).                                                                                                |

Durations use the [Go syntax](https://pkg.go.dev/time#ParseDuration) (`50ms`, `5m`, `1h`).

## How it works

### Features

- **Methods**: `GET` responses are stored, `HEAD` requests are answered from them.
- **Freshness**: `s-maxage`, `max-age`, `Expires` and `Age`, plus a default TTL for responses that declare none.
- **`Cache-Control`**: `no-store`, `no-cache`, `private`, `public`, `must-revalidate`, `must-understand` and `proxy-revalidate` on
  responses; `no-cache`, `no-store`, `max-age`, `min-fresh`, `max-stale` and `only-if-cached` on requests.
- **`Vary`**: one stored variant per set of request header values, capped per URI; for `Accept-Encoding`, one per `Content-Encoding`, shared
  by every client that accepts it.
- **Revalidation**: `ETag` / `If-None-Match`, `Last-Modified` / `If-Modified-Since` and `304 Not Modified`, towards clients and towards the
  backend.
- **Stale responses**: `stale-while-revalidate`, `stale-if-error`, and when the backend is unreachable.
- **Invalidation**: a successful unsafe request (`POST`, `PUT`, `DELETE`…) invalidates the stored responses of its URI.
- **Shared-cache safety**: responses to `Authorization` requests or with `Set-Cookie` are stored only when the backend explicitly allows it
  (`private="Set-Cookie"`), and cookies are never replayed.
- **Cache key**: scheme, host, path and query. With `sortQuery`, `?a=1&b=2` and `?b=2&a=1` share the same entry.
- **`Cache-Status`**: every response tells what the cache did (`hit; ttl=N`, `fwd=uri-miss`, `fwd=vary-miss`, `fwd=bypass`…).
- **Fail open**: any Redis error or timeout bypasses the cache, and Traefik starts even when Redis is down.
- **Streaming**: responses larger than `maxBodyBytes`, WebSockets and server-sent events pass through untouched.

### What gets cached

A response is stored only when all of the following hold:

- the request is a `GET` (a `HEAD` is answered from the stored `GET`);
- its status is listed in `statusCodes`;
- it is fresh, through `s-maxage`, `max-age`, `Expires` or `defaultTtl`;
- neither side says `no-store`, and the response is not `private` or `no-cache`;
- a request with `Authorization` got a response with `public`, `s-maxage` or `must-revalidate`;
- a response with `Set-Cookie` says `private="Set-Cookie"` or `no-cache="Set-Cookie"`: it is stored without the cookie, which only the
  client that caused the miss gets. Without one of them, it is not stored, even `public`;
- its `Vary`, if any, is not `*` and names neither `Cookie` nor `Authorization` (see [Variants](#variants-vary));
- it is complete: a response that the backend cuts short is not stored;
- it fits in `maxBodyBytes`.

Requests that are never cached (`OPTIONS`, `TRACE`, WebSocket upgrades, `Accept: text/event-stream`) are forwarded as is, with
`Cache-Status: traedis; fwd=method` or `fwd=bypass`. Unsafe methods (`POST`, `PUT`, `DELETE`…) are forwarded too, and
[invalidate](#invalidation) what is stored.

### Variants (`Vary`)

When a response has a `Vary` header, the cache keeps one variant per value of the request headers it names
([RFC 9111 §4.1](https://www.rfc-editor.org/rfc/rfc9111#section-4.1)): with `Vary: Accept-Language`, requests with `Accept-Language: fr` and
`Accept-Language: en` each get their own stored response. A request for a known URL whose variant is not stored is forwarded with
`Cache-Status: traedis; fwd=vary-miss`.

- The backend decides: there is no option to add or ignore a header, and the `Vary` sent to clients is never rewritten.
- Values are compared as they are, apart from surrounding spaces and repeated header lines: `fr-FR` and `fr-fr` are two variants, and a
  request without the header has its own.
- At most `maxVariants` variants (16 by default) are stored per URL. Past that, other variants are forwarded and not stored: stored ones are
  never evicted to make room, not even when `maxVariants` is lowered. Clients choose the values of these headers: the worst case for one URL
  is `maxVariants` × `maxBodyBytes` in Redis.
- `Vary: *`, and a `Vary` naming `Cookie` or `Authorization` (one variant per user), are not cached.

`Accept-Encoding` is the exception, because clients spell it in many ways (`gzip, deflate, br, zstd`, `gzip,br`, `br;q=1.0, gzip;q=0.8`…).
The request reaches the backend untouched, and its response is stored by `Content-Encoding` (`zstd`, `br`, `gzip`, `deflate` or none): one
variant per coding the backend produces, served to every request that accepts it.

- A request never gets a coding it does not accept (`q=0` and `*` are honoured; without `Accept-Encoding`, only an uncompressed response),
  and clients cannot create variants by changing their `Accept-Encoding`.
- When several stored codings are accepted, the first of `zstd`, `br`, `gzip`, `deflate` is served, whatever the other weights of the
  request. It may not be the coding the backend would have chosen: a browser gets the stored `gzip` variant, even from a backend that would
  have sent it `br`, until that variant is stale.
- An uncompressed response is only served to requests accepting no more codings than the request it was stored for. A `curl` without
  `Accept-Encoding` cannot make browsers get the uncompressed response: their requests are forwarded, and the backend may compress for them.
- A response with `Vary: Accept-Encoding` and another `Content-Encoding`, or several, is not cached.

> [!NOTE]
> Traefik's `compress` middleware only adds `Vary: Accept-Encoding` to the responses it compresses. Placed after the cache, its uncompressed
> response to a request without `Accept-Encoding` has no `Vary`, and is then served to everyone. Put `compress` before the cache, or set
> `Vary: Accept-Encoding` on every response with a `headers` middleware, as [`example/dynamic/whoami.yml`](example/dynamic/whoami.yml) does.

### Conditional requests (`ETag`, `Last-Modified`)

A client that already has a response asks for it with `If-None-Match` or `If-Modified-Since`, and gets a `304 Not Modified` without body
when its copy is still current ([RFC 9111 §4.3](https://www.rfc-editor.org/rfc/rfc9111#section-4.3)). The cache plays both parts.

**It answers the conditions of its clients from the responses it has stored**: a `304` when the `ETag` of the stored response is one of
`If-None-Match` (weak comparison), or when the response was not modified since `If-Modified-Since` (its `Last-Modified`, otherwise its
`Date`).

- Without a stored response, the conditions are for the backend: the request reaches it as it is, and its `304` goes to the client
  (`Cache-Status: traedis; fwd=uri-miss; fwd-status=304`). Nothing is stored then: a URL is cached once a client asks for it without having
  it already.
- `If-Match`, `If-Unmodified-Since` and `If-Range` are always for the backend.

**It validates its stale responses with the backend.** When a stored response may no longer be served (stale, or too old for the `max-age`
of the request) and has an `ETag` or a `Last-Modified`, the backend gets them in `If-None-Match` and `If-Modified-Since`, in place of the
conditions of the client.

- A `304` has no body: the stored response is served, with the header fields of the `304` in place of its own, and is fresh again
  (`Cache-Status: traedis; fwd=stale; fwd-status=304; ttl=60`). The client gets a `304` in turn when it already has that response.
- Any other response is sent to the client as it is and replaces the stored one.
- A `304` has to name the validator of the stored response to update it: without any, the stored response is served but stays stale; with
  another one, the backend is asked again for a whole response.
- The background refresh of `stale-while-revalidate` validates in the same way.
- Without `ETag` nor `Last-Modified`, a stale response is fetched again as a whole.

A `HEAD` request is never made conditional. When its response has the `ETag` or the `Last-Modified` of the stored response (and its length),
the stored response gets its header fields and is fresh again; when they differ, a stored response that was still fresh is dropped.

What is not done: responses are not stored only to be validated (`no-cache`, `max-age=0` and responses that are already expired are still
not cached), and a variant that is not stored is fetched as a whole, without the `ETag` of the other variants.

### Stale responses

A stored response that is no longer fresh is served only when the backend allows it ([RFC 5861](https://www.rfc-editor.org/rfc/rfc5861)),
for at most `staleTtl`:

- **`stale-while-revalidate=N`**: for `N` seconds, the stale response is served at once and refreshed in the background
  (`Cache-Status: traedis; hit; ttl=-12; detail=stale-while-revalidate`). At most one refresh per URL and 10 at a time run in each Traefik
  instance.
- **`stale-if-error=N`**: for `N` seconds, the stale response replaces a `500`, `502`, `503` or `504`, including those of Traefik when the
  backend is unreachable (`Cache-Status: traedis; fwd=stale; fwd-status=503; ttl=-12; detail=stale-if-error`).

As RFC 9111 requires, `must-revalidate`, `proxy-revalidate`, `s-maxage` and `no-cache` forbid stale responses:
`max-age=60, stale-while-revalidate=300` is served stale, `s-maxage=60, stale-while-revalidate=300` is not.

A request with `max-age` or `min-fresh` (a browser reload) always goes to the backend, and still gets the stale response if it fails with
`stale-if-error`. A request with `no-cache` never gets a stored response.

A client can also ask for a stale response itself, with the request directive **`max-stale`**
([RFC 9111 §5.2.1.2](https://www.rfc-editor.org/rfc/rfc9111#section-5.2.1.2)): `max-stale=N` accepts a response that is stale by less than
`N` seconds, `max-stale` alone by any time, within `staleTtl` (`Cache-Status: traedis; hit; ttl=-12; detail=max-stale`). The response is
served as it is, without asking the backend nor refreshing it. `must-revalidate`, `proxy-revalidate`, `s-maxage` and `no-cache` still forbid
it. With `only-if-cached`, it is what an offline client sends. When nothing stored can answer an `only-if-cached` request, it gets a `504`
without `Cache-Status`: the request is not forwarded.

For backends that don't send these directives, `defaultStaleWhileRevalidate` and `defaultStaleIfError` set the windows instead:

```yaml
cache:
  plugin:
    traedis:
      defaultStaleIfError: 10m # serve what is stored rather than a 5xx
      defaultStaleWhileRevalidate: 30s
```

They only apply to responses without the directive (`stale-if-error=0` from the backend still turns it off), never against
`must-revalidate`, `proxy-revalidate`, `s-maxage` or `no-cache`, and within `staleTtl`. With `defaultStaleWhileRevalidate`, clients can get
a response older than what the backend asked for: on a URL that is rarely requested, by up to the whole window.

### Invalidation

A request with an unsafe method (`POST`, `PUT`, `PATCH`, `DELETE`, or any method that is not `GET`, `HEAD`, `OPTIONS` or `TRACE`) may change
what its URL returns. When the backend answers it with a `2xx` or a `3xx`, the stored responses of that URL are removed, with all their
variants ([RFC 9111 §4.4](https://www.rfc-editor.org/rfc/rfc9111#section-4.4)):
`Cache-Status: traedis; fwd=method; fwd-status=204; detail=invalidated`.

- They are removed before the client gets the answer: its next `GET` is not served what was stored before.
- An error (`4xx`, `5xx`) changes nothing: the stored responses are kept, also for `stale-if-error`.
- Only the URL of the request is invalidated, query included: `POST /cart/add` does not invalidate `/cart`, and the `Location` and
  `Content-Location` of the response are not followed.
- If Redis cannot be reached at that moment (`detail=redis`), the stored responses stay until they expire.
- A `GET` that was already on its way to the backend can store its response after the invalidation.

The responses to these requests are never held back: they are sent to the client as the backend writes them.

## Security

A shared cache gives the response made for one client to all the others. The defaults are careful (see
[What gets cached](#what-gets-cached)), but the cache only knows what the backend and the configuration tell it:

- **A cookie does not make a response private.** `defaultTtl` is never applied to an exchange with `Authorization`, `Cookie` or
  `Set-Cookie`, but a response with `max-age`, `s-maxage` or `Expires` is stored and shared even when its request had a `Cookie`: cookies
  are not part of the cache key. Whatever depends on the session must say `Cache-Control: private` or `no-store`. In the same way, `public`,
  `s-maxage` and `must-revalidate` allow sharing the response to a request with `Authorization`.
- **`private="Set-Cookie"` only keeps the cookie out.** A response with `Set-Cookie` is not stored, even `public`, unless it says
  `private="Set-Cookie"` or `no-cache="Set-Cookie"`. Its other header fields and its body are then served to everyone: don't use it when
  they depend on the cookie (CSRF token, user name…).
- **Middlewares listed after the cache don't run on a hit.** `basicAuth`, `forwardAuth`, `ipAllowList` and `rateLimit` go before it. The
  cache does not know who they let in: what it stores is shared by all of them.
- **What the response depends on must be in `Vary`.** When the backend reads a request header that its `Vary` does not name
  (`X-Forwarded-Host`, `User-Agent`, `Accept-Language`…), one client chooses what the others get. This goes for the scheme too: it is the
  one of the connection to Traefik, `X-Forwarded-Proto` is not trusted. Behind a proxy that terminates TLS, `http` and `https` requests
  share their stored responses.
- **Clients can always reach the backend, and fill Redis.** A random query string is a miss and a new entry, `Cache-Control: no-cache` skips
  the cache: put a `rateLimit` before it, and give Redis a `maxmemory` with the `allkeys-lfu` policy.
- **Redis is trusted.** Whoever writes to it chooses what clients are served, whoever reads it gets the stored responses, and the connection
  is not encrypted, password included: keep it on a private network, for the cache only.

## Development

Requires Docker (with Compose), Go ≥ 1.22 and the [Yaegi](https://github.com/traefik/yaegi) version embedded in Traefik v3.7:
`go install github.com/traefik/yaegi/cmd/yaegi@v0.16.1`.

### Project structure

```text
pkg/          # the plugin (package `traedis`): one concern per file, with its `_test.go` next to it
example/      # Traefik configuration of the local stack: one dynamic file per sample backend
e2e/          # end-to-end tests: Hurl files requesting Traefik
benchmark/    # benchmarks under Yaegi, k6 load test, results viewer
compose.yml   # local stack: Traefik, Redis, sample backends (whoami, placeholder, imgproxy), Redis Insight
.traefik.yml  # plugin manifest of the Traefik Plugin Catalog
VERSION       # the version to release
```

In `pkg/`, `middleware.go` is the entry point (`New()` and the handler), `policy.go` decides what may be stored and served, and `redis.go`
is the Redis client (RESP and connection pool).

### Commands

```bash
make start       # start the local stack; Traefik restarts whenever the plugin code changes
make stop        # stop the stack (`make reset` also removes the volumes)
make unit        # go vet, go test -race, then the same tests under Yaegi, with a Redis for the integration tests
make e2e         # end-to-end tests, through Traefik
make bench       # benchmarks → benchmark/results.json (`make bench-view` shows them)
make help        # list all targets
```

`make start` prints the URLs to try: Traefik listens on <http://localhost:8080>, each sample backend has a route without the cache and one
with it, and Redis Insight shows what is stored.

### Rules

- **Yaegi runs the plugin, not the Go compiler.** Only the standard library of Go 1.22: no third-party module (not even a Redis client), no
  `unsafe`, no cgo, no generics. Code that compiles can still fail under the interpreter: `yaegi test` has to pass, not only `go test`.
- **RFC 9111 to the letter, as a shared cache.** Don't "optimise" its rules. A departure from it is an option that is off by default, like
  `sortQuery`.
- **Fail open.** A Redis error or timeout forwards the request, and `New()` succeeds when Redis is unreachable.
- **Never break a response.** Nothing is buffered past `maxBodyBytes`, and streaming (`Flush`), WebSockets (`Hijack`) and trailers keep
  working.
- **Clients are not trusted, neither is what Redis returns.** Clients must not be able to multiply entries without bound, nor to evict
  stored ones other than by [invalidation](#invalidation), and nothing is allocated from a length that was not checked.
- **Stored formats are stable.** Changing the Redis key invalidates the whole cache; changing the encoding of an entry or of a `Vary` marker
  bumps its version prefix.
- **Every bug fix comes with a regression test**, and what is visible through Traefik with an end-to-end one.
- **Keep it simple.** No option, dependency or abstraction without a need for it.

### Releases

The version is the content of `VERSION`: on `main`, the CI checks that it is the next patch, minor or major of the last release.

## License

[MIT](LICENSE)

[ci]: https://github.com/branchard/traedis/actions/workflows/ci.yml

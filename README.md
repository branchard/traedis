> [!WARNING]
> **This project is a work in progress.** Use it at your own risk.

<p align="center">
  <img src="assets/icon.png" alt="Traedis Logo" width="480">
</p>

# Traedis

[![CI](https://github.com/branchard/traedis/actions/workflows/ci.yml/badge.svg)](https://github.com/branchard/traedis/actions/workflows/ci.yml)

Redis-backed HTTP cache for Traefik, shipped as a **Traefik v3 middleware plugin**: it stores backend
responses in Redis and serves them according to [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111)
(HTTP caching, as a shared cache).

- **Standards-based**: honors `Cache-Control` (`max-age`, `s-maxage`, `no-store`, `private`, `no-cache`…), `Expires` and `Age`, on both requests and responses.
- **Safe by default**: never caches what must not be shared (`Authorization`, `Set-Cookie`, `private`) unless the backend explicitly allows it.
- **Fail open**: if Redis is slow or down, requests go straight to the backend.
- **Observable**: every response carries a [`Cache-Status`](https://www.rfc-editor.org/rfc/rfc9211) header (`hit`, `fwd=uri-miss`, `fwd=bypass`…).
- **Stream-friendly**: large bodies, WebSockets and server-sent events pass through untouched.

> [!NOTE]
> Traedis is at an early stage. `GET`/`HEAD` caching with RFC 9111 freshness works end to end.
> Not implemented yet: `Vary`, serving stale responses, revalidation and invalidation.
> Until then, responses with a `Vary` header are not cached.

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

## Example

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

A complete local setup (Traefik, Redis, sample backends) is available in [`compose.yml`](compose.yml)
and [`example/`](example).

## Configuration

All keys are optional.

| Key             | Default                | Description                                                                                                                                  |
|-----------------|------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `redis.dsn`     | `redis://cache:6379/0` | Redis connection string: `redis://[user[:password]@]host[:port][/db]`. TLS (`rediss://`) is not supported yet.                               |
| `redis.timeout` | `50ms`                 | Maximum time for a Redis operation. Past it, the cache is bypassed.                                                                          |
| `statusCodes`   | `[200]`                | Status codes allowed to be stored. It only narrows what RFC 9111 allows; it never makes a response cacheable.                                |
| `defaultTtl`    | `5m`                   | Freshness when the backend sends no `max-age`, `s-maxage` or `Expires`. `0` disables it. Never applied to requests with `Authorization` or `Cookie`, nor to responses with `Set-Cookie`. |
| `staleTtl`      | `1h`                   | How long an entry is kept in Redis after it expires. Serving stale responses is not implemented yet.                                         |
| `maxBodyBytes`  | `5242880` (5 MiB)      | Larger responses are streamed to the client and not cached.                                                                                  |
| `vary`          | `[]`                   | Request headers to cache separate variants for. `Cookie` and `Authorization` are rejected. Not implemented yet: a non-empty list disables caching. |
| `exposeKey`     | `false`                | Add the cache key to the `Cache-Status` header (`key="…"`).                                                                                  |

Durations use the [Go syntax](https://pkg.go.dev/time#ParseDuration) (`50ms`, `5m`, `1h`).

## What gets cached

A response is stored only when all of the following hold:

- the request is a `GET` (a `HEAD` is answered from the stored `GET`);
- its status is listed in `statusCodes`;
- it is fresh, through `s-maxage`, `max-age`, `Expires` or `defaultTtl`;
- neither side says `no-store`, and the response is not `private` or `no-cache`;
- a request with `Authorization` got a response with `public`, `s-maxage` or `must-revalidate`;
- a response with `Set-Cookie` is `public` (the cookie itself is never stored or replayed);
- it fits in `maxBodyBytes`.

Requests that are never cached (other methods, WebSocket upgrades, `Accept: text/event-stream`) are
forwarded as is, with `Cache-Status: traedis; fwd=method` or `fwd=bypass`.

## Development

Requires Docker (with Compose), Go and the [Yaegi](https://github.com/traefik/yaegi) version embedded in Traefik v3.7: `go install github.com/traefik/yaegi/cmd/yaegi@v0.16.1`.

```bash
make start  # start the local stack; Traefik restarts whenever the plugin code changes
make stop   # stop the stack (`make clean` also removes the volumes)
make unit   # go vet, then unit and Redis integration tests, compiled and under Yaegi
make e2e    # end-to-end tests, through Traefik
make help   # list all targets
```

Once started, Traefik listens on <http://localhost:8080>: try `curl -i http://localhost:8080/600x400` twice and watch the `Cache-Status` header.

## License

[MIT](LICENSE)

> [!WARNING]
> **This project is a work in progress.** Use it at your own risk.

<p align="center">
  <img src="assets/icon.svg" alt="Traedis Logo" height="360">
</p>

# Traedis

[![CI](https://github.com/branchard/traedis/actions/workflows/ci.yml/badge.svg)](https://github.com/branchard/traedis/actions/workflows/ci.yml)

**Redis**-backed HTTP cache for **Traefik**, shipped as a **middleware plugin**.

- **Standards-based**: follows [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111) (`Cache-Control`, `ETag`, `Last-Modified`, `304 Not Modified`…), [RFC 5861](https://www.rfc-editor.org/rfc/rfc5861) (`stale-while-revalidate`, `stale-if-error`) and [RFC 9211](https://www.rfc-editor.org/rfc/rfc9211) (`Cache-Status`).
- **Safe by default**: never caches what must not be shared (`Authorization`, `Set-Cookie`, `private`) unless the backend explicitly allows it.
- **Fail open**: if Redis is slow or down, requests go straight to the backend.
- **Observable**: every response carries a `Cache-Status` header (`hit`, `fwd=uri-miss`, `fwd=bypass`…).
- **Stream-friendly**: large bodies, WebSockets and server-sent events pass through untouched.

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

A complete local setup (Traefik, Redis, sample backends) is available in [`compose.yml`](compose.yml)
and [`example/`](example).

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
| `exposeKey`                   | `false`                | Add the cache key to the `Cache-Status` header (`key="…"`).                                                                                                |

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
make help   # list all targets
```

Once started, Traefik listens on <http://localhost:8080>: try `curl -i http://localhost:8080/placeholder-cache/600x400` twice and watch the `Cache-Status` header (`/placeholder/600x400` is the same backend without the cache).

## License

[MIT](LICENSE)

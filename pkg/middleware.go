package traedis

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// entryOverhead is the room left for headers and metadata in a stored entry,
// on top of maxBodyBytes.
const entryOverhead = 1 << 20

// cache is the middleware handler.
type cache struct {
	next     http.Handler
	name     string
	cfg      settings
	store    store
	maxEntry int64
}

// New creates the middleware. It never contacts Redis: Traefik must start even
// when Redis is unreachable.
func New(_ context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	c := newCache(next, name, cfg, nil)
	c.store = newRedisClient(cfg.redis, c.maxEntry)
	return c, nil
}

func newCache(next http.Handler, name string, cfg settings, s store) *cache {
	return &cache{next: next, name: name, cfg: cfg, store: s, maxEntry: cfg.maxBodyBytes + entryOverhead}
}

func (c *cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		c.passThrough(w, r, "method")
		return
	}
	if expectsStream(r) {
		c.passThrough(w, r, "bypass")
		return
	}

	key := redisKey(cacheURI(r))
	status := cacheStatus{}
	if c.cfg.exposeKey {
		status.key = key
	}
	reqCC := parseCacheControl(r.Header.Values("Cache-Control"))

	if !lookupAllowed(r) {
		status.fwd = "request"
		c.forward(w, r, key, reqCC, status, true)
		return
	}

	raw, err := c.store.get(r.Context(), key, "")
	if err != nil && !errors.Is(err, errMiss) {
		// Fail open; don't try to write to a failing Redis.
		status.fwd = "bypass"
		status.detail = "redis"
		c.forward(w, r, key, reqCC, status, false)
		return
	}
	status.fwd = "uri-miss"
	if err == nil {
		if e, err := decodeEntry(raw, c.maxEntry); err == nil {
			age := currentAge(e.header, e.requestTime, e.responseTime, time.Now())
			lifetime := freshnessLifetime(e, c.cfg.defaultTTL)
			if servable(reqCC, age, lifetime) {
				c.serve(w, r, e, age, lifetime, status)
				return
			}
			status.fwd = "stale"
		}
	}
	c.forward(w, r, key, reqCC, status, true)
}

// passThrough proxies a request the cache never handles, without wrapping the
// writer (Yaegi would hide http.Flusher from the backend). Cache-Status is set
// before the backend's own values.
func (c *cache) passThrough(w http.ResponseWriter, r *http.Request, fwd string) {
	w.Header().Add("Cache-Status", cacheStatus{fwd: fwd}.String())
	c.next.ServeHTTP(w, r)
}

// expectsStream reports requests whose responses must be flushed as they come:
// they are never wrapped (see passThrough).
func expectsStream(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	for _, v := range r.Header.Values("Accept") {
		if strings.Contains(strings.ToLower(v), "text/event-stream") {
			return true
		}
	}
	return false
}

// serve answers from a stored entry (§4).
func (c *cache) serve(w http.ResponseWriter, r *http.Request, e *entry, age, lifetime time.Duration, status cacheStatus) {
	h := w.Header()
	for name, values := range e.header {
		h[name] = append([]string(nil), values...)
	}
	h.Set("Age", strconv.FormatInt(int64(age/time.Second), 10))
	if bodyAllowed(e.status) {
		h.Set("Content-Length", strconv.Itoa(len(e.body)))
	}
	status.hit = true
	status.ttl = lifetime - age
	h.Add("Cache-Status", status.String())
	w.WriteHeader(e.status)
	if r.Method != http.MethodHead && bodyAllowed(e.status) {
		_, _ = w.Write(e.body)
	}
}

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}

// forward proxies the request to the backend and stores the response when allowed.
func (c *cache) forward(w http.ResponseWriter, r *http.Request, key string, reqCC cacheControl, status cacheStatus, mayStore bool) {
	if reqCC.onlyIfCached {
		// §5.2.1.7: no stored response may be used.
		w.Header().Add("Cache-Status", status.String())
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	m := &missHook{c: c, req: r, status: status, store: mayStore && r.Method == http.MethodGet, requestTime: time.Now()}
	rec := newRecorder(w, m, c.cfg.maxBodyBytes)
	// The original request goes to the backend: context and trace headers intact.
	c.next.ServeHTTP(rec, r)
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}

	body, complete := rec.body()
	if !complete || !m.store {
		return
	}
	data := encodeEntry(&entry{
		status:       rec.status,
		header:       m.header,
		body:         body,
		requestTime:  m.requestTime,
		responseTime: m.responseTime,
		vary:         http.Header{},
	})
	if int64(len(data)) > c.maxEntry {
		return
	}
	// The client gets the whole response before the Redis write.
	rec.Flush()
	_ = c.store.set(context.WithoutCancel(r.Context()), key, "", data, m.ttl)
}

// missHook decides, when the backend sends its final status, whether the
// response is stored, and adds Cache-Status.
type missHook struct {
	c            *cache
	req          *http.Request
	status       cacheStatus
	store        bool
	requestTime  time.Time
	responseTime time.Time
	header       http.Header // header to store, without our Cache-Status
	ttl          time.Duration
}

func (m *missHook) beforeHeader(code int, h http.Header) bool {
	m.responseTime = time.Now()
	if m.store {
		ttl, ok := storeTTL(m.req, code, h, m.c.cfg, m.requestTime, m.responseTime)
		m.store = ok
		if ok {
			m.ttl = ttl
			m.header = headerToStore(h)
		}
	}
	m.status.fwdStatus = code
	h.Add("Cache-Status", m.status.String())
	return m.store
}

package traedis

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

	revalMu      sync.Mutex
	revalidating map[string]bool // keys being revalidated, at most maxRevalidations
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
	return &cache{
		next: next, name: name, cfg: cfg, store: s, maxEntry: cfg.maxBodyBytes + entryOverhead,
		revalidating: map[string]bool{},
	}
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
		c.forward(w, r, key, reqCC, status, true, nil)
		return
	}

	field := ""
	status.fwd = "uri-miss"
	raw, err := c.store.get(r.Context(), key, "")
	if mk, ok := decodeMarker(raw); err == nil && ok {
		// The responses of this URI vary: the one selected by the request, if
		// any, is in a field of its own.
		status.fwd = "vary-miss"
		field = mk.field(r.Header)
		err = errMiss
		if field != "" {
			raw, err = c.store.get(r.Context(), key, field)
		}
	}
	if err != nil && !errors.Is(err, errMiss) {
		// Fail open; don't try to write to a failing Redis.
		status.fwd = "bypass"
		status.detail = "redis"
		c.forward(w, r, key, reqCC, status, false, nil)
		return
	}
	var stored *entry
	if err == nil {
		if e, err := decodeEntry(raw, c.maxEntry); err == nil && selects(e, r.Header) {
			cc := parseCacheControl(e.header.Values("Cache-Control"))
			age := currentAge(e.header, e.requestTime, e.responseTime, time.Now())
			lifetime := freshnessLifetime(e, cc, c.cfg.defaultTTL)
			if servable(reqCC, age, lifetime) {
				status.hit = true
				c.serve(w, r, e, age, lifetime, status)
				return
			}
			if servableWhileRevalidating(reqCC, cc, age-lifetime, c.cfg) {
				c.revalidate(r, key, field)
				status.hit = true
				status.detail = "stale-while-revalidate"
				c.serve(w, r, e, age, lifetime, status)
				return
			}
			status.fwd = "stale"
			stored = e
		}
	}
	c.forward(w, r, key, reqCC, status, true, stored)
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

// serve answers from a stored entry (§4), fresh or stale.
func (c *cache) serve(w http.ResponseWriter, r *http.Request, e *entry, age, lifetime time.Duration, status cacheStatus) {
	h := w.Header()
	for name, values := range e.header {
		h[name] = append([]string(nil), values...)
	}
	h.Set("Age", strconv.FormatInt(int64(age/time.Second), 10))
	if bodyAllowed(e.status) {
		h.Set("Content-Length", strconv.Itoa(len(e.body)))
	}
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

// forward proxies the request to the backend and stores the response when
// allowed. stored is the entry that could not be served, if any: it answers in
// place of a backend error when stale-if-error allows it (RFC 5861 §4).
func (c *cache) forward(w http.ResponseWriter, r *http.Request, key string, reqCC cacheControl, status cacheStatus, mayStore bool, stored *entry) {
	if reqCC.onlyIfCached {
		// §5.2.1.7: no stored response may be used.
		w.Header().Add("Cache-Status", status.String())
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	m := &missHook{c: c, req: r, status: status, store: mayStore && r.Method == http.MethodGet, requestTime: time.Now(), stored: stored}
	var before http.Header
	if stored != nil {
		// What the header holds now, without what the backend will add to it.
		before = w.Header().Clone()
	}
	rec := newRecorder(w, m, c.cfg.maxBodyBytes)
	// The original request goes to the backend: context and trace headers intact.
	c.next.ServeHTTP(rec, r)
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}

	if m.staleIfError {
		// Nothing of the backend's error was sent: the stored response replaces it.
		h := w.Header()
		for name := range h {
			delete(h, name)
		}
		for name, values := range before {
			h[name] = values
		}
		status.fwdStatus = rec.status
		status.served = true
		status.detail = "stale-if-error"
		c.serve(w, r, stored, m.age, m.lifetime, status)
		return
	}
	c.save(context.WithoutCancel(r.Context()), rec, m, key)
}

// save stores the recorded response when the hook allowed it and its body was
// fully captured. It reports whether the response was sent to the store.
func (c *cache) save(ctx context.Context, rec *recorder, m *missHook, key string) bool {
	body, complete := rec.body()
	if !complete || !m.store {
		return false
	}
	data := encodeEntry(&entry{
		status:       rec.status,
		header:       m.header,
		body:         body,
		requestTime:  m.requestTime,
		responseTime: m.responseTime,
		vary:         m.variant.selected,
	})
	if int64(len(data)) > c.maxEntry {
		return false
	}
	// The client gets the whole response before the Redis writes.
	rec.Flush()
	if m.variant.field == "" {
		_ = c.store.set(ctx, key, "", data, m.ttl)
	} else {
		c.saveVariant(ctx, key, m.variant, data, m.ttl)
	}
	return true
}

// saveVariant stores a variant and the marker of its key. Any Redis error ends
// it: nothing is written to a failing Redis.
func (c *cache) saveVariant(ctx context.Context, key string, v variant, data []byte, ttl time.Duration) {
	mk := marker{names: v.names}
	if v.coding != "" {
		// The marker lists the codings stored so far. Reading it and writing it
		// back is not atomic: a coding lost to a concurrent write is a miss for
		// the requests it would serve, whose response lists it again.
		mk.codings = []string{v.coding}
		raw, err := c.store.get(ctx, key, "")
		if err != nil && !errors.Is(err, errMiss) {
			return
		}
		if old, ok := decodeMarker(raw); ok && sameStrings(old.names, v.names) {
			mk.codings = v.listed(old.codings)
		}
	}
	n, err := c.store.count(ctx, key)
	if err != nil {
		return
	}
	// A full key (maxVariants and the marker) only gets its variants replaced.
	// The count is not atomic either: concurrent writes may exceed the limit
	// by a few fields.
	_ = c.store.setVariant(ctx, key, v.field, data, encodeMarker(mk), ttl, n > c.cfg.maxVariants)
}

// missHook decides, when the backend sends its final status, what becomes of
// the response (sent, stored, replaced by a stale entry), and adds Cache-Status.
type missHook struct {
	c            *cache
	req          *http.Request
	status       cacheStatus
	store        bool
	requestTime  time.Time
	responseTime time.Time
	header       http.Header // header to store, without our Cache-Status
	ttl          time.Duration
	variant      variant // where to store it

	// stale-if-error: the entry that may replace a backend error and, once
	// staleIfError is set, its age and freshness lifetime.
	stored       *entry
	staleIfError bool
	age          time.Duration
	lifetime     time.Duration

	// Background revalidation: superseded is set when the entry must not
	// outlive a response that ends up not being stored.
	revalidation bool
	superseded   bool
}

func (m *missHook) beforeHeader(code int, h http.Header) disposition {
	m.responseTime = time.Now()
	if m.stored != nil && staleIfErrorStatus(code) {
		cc := parseCacheControl(m.stored.header.Values("Cache-Control"))
		m.age = currentAge(m.stored.header, m.stored.requestTime, m.stored.responseTime, m.responseTime)
		m.lifetime = freshnessLifetime(m.stored, cc, m.c.cfg.defaultTTL)
		if servableOnError(code, cc, m.age-m.lifetime, m.c.cfg) {
			m.staleIfError = true
			return withhold
		}
	}
	if m.store {
		ttl, ok := storeTTL(m.req, code, h, m.c.cfg, m.requestTime, m.responseTime)
		m.store = ok
		if ok {
			m.ttl = ttl
			m.header = headerToStore(h)
			m.variant = newVariant(m.req, h)
		}
	}
	if m.revalidation {
		// A storable response whose body turns out to be too large supersedes too.
		m.superseded = m.store || supersedes(code, h, m.c.cfg, m.requestTime, m.responseTime)
	}
	m.status.fwdStatus = code
	h.Add("Cache-Status", m.status.String())
	if m.store {
		return keepCopy
	}
	return passOn
}

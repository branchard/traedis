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
	if !safeMethod(r.Method) {
		c.invalidate(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		c.passThrough(w, r, "method")
		return
	}
	if expectsStream(r) {
		c.passThrough(w, r, "bypass")
		return
	}

	key := redisKey(cacheURI(r, c.cfg.sortQuery))
	status := cacheStatus{}
	if c.cfg.exposeKey {
		status.key = key
	}
	reqCC := parseCacheControl(r.Header.Values("Cache-Control"))

	if !lookupAllowed(r) {
		status.fwd = "request"
		c.forward(w, r, key, "", reqCC, status, true, nil)
		return
	}

	stored, field, miss, err := c.find(r.Context(), key, r.Header)
	if err != nil {
		// Fail open; don't try to write to a failing Redis.
		status.fwd = "bypass"
		status.detail = "redis"
		c.forward(w, r, key, "", reqCC, status, false, nil)
		return
	}
	status.fwd = miss
	if stored != nil {
		cc := parseCacheControl(stored.header.Values("Cache-Control"))
		age := currentAge(stored.header, stored.requestTime, stored.responseTime, time.Now())
		lifetime := freshnessLifetime(stored, cc, c.cfg.defaultTTL)
		if servable(reqCC, age, lifetime) {
			status.hit = true
			c.serve(w, r, stored, age, lifetime, status)
			return
		}
		if servableWhileRevalidating(reqCC, cc, age-lifetime, c.cfg) {
			c.revalidate(r, key, field, stored)
			status.hit = true
			status.detail = "stale-while-revalidate"
			c.serve(w, r, stored, age, lifetime, status)
			return
		}
		if servableOnRequest(reqCC, cc, age, lifetime, c.cfg.staleTTL) {
			status.hit = true
			status.detail = "max-stale"
			c.serve(w, r, stored, age, lifetime, status)
			return
		}
		// RFC 9211 §2.2: the response is stale, or fresh but not what the
		// request asks for (max-age, min-fresh).
		status.fwd = "stale"
		if age < lifetime {
			status.fwd = "request"
		}
	}
	c.forward(w, r, key, field, reqCC, status, true, stored)
}

// find returns the stored response of key selected by the request header h
// (§4.1) and the field it is in, or none. miss is then why: uri-miss, or
// vary-miss when the key holds variants. An error is a Redis failure.
func (c *cache) find(ctx context.Context, key string, h http.Header) (*entry, string, string, error) {
	field := ""
	miss := "uri-miss"
	raw, err := c.store.get(ctx, key, "")
	if mk, ok := decodeMarker(raw); err == nil && ok {
		// The responses of this URI vary: the one selected by the request, if
		// any, is in a field of its own.
		miss = "vary-miss"
		field = mk.field(h)
		err = errMiss
		if field != "" {
			raw, err = c.store.get(ctx, key, field)
		}
	}
	if errors.Is(err, errMiss) {
		return nil, field, miss, nil
	}
	if err != nil {
		return nil, field, miss, err
	}
	e, err := decodeEntry(raw, c.maxEntry)
	if err != nil || !selects(e, h) {
		return nil, field, miss, nil
	}
	return e, field, miss, nil
}

// passThrough proxies a request the cache has nothing to do with (OPTIONS,
// TRACE, upgrades, event streams), without wrapping the writer (Yaegi would
// hide http.Flusher from the backend). Cache-Status is set before the
// backend's own values.
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

// serve answers from a stored entry (§4), fresh or stale: with a 304 when the
// client says it already has it (§4.3.2).
func (c *cache) serve(w http.ResponseWriter, r *http.Request, e *entry, age, lifetime time.Duration, status cacheStatus) {
	h := w.Header()
	for name, values := range e.header {
		h[name] = append([]string(nil), values...)
	}
	if e.header.Get("Date") == "" {
		// RFC 9110 §6.6.1: a response received without Date gets the time it
		// was received.
		h.Set("Date", e.responseTime.UTC().Format(http.TimeFormat))
	}
	h.Set("Age", strconv.FormatInt(int64(age/time.Second), 10))
	status.ttl = lifetime - age
	h.Add("Cache-Status", status.String())
	if e.status == http.StatusOK && notModified(r.Header, e.header, e.responseTime) {
		notModifiedHeader(h)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if bodyAllowed(e.status) {
		h.Set("Content-Length", strconv.Itoa(len(e.body)))
	}
	w.WriteHeader(e.status)
	if r.Method != http.MethodHead && bodyAllowed(e.status) {
		_, _ = w.Write(e.body)
	}
}

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}

// forward proxies the request to the backend and stores the response when
// allowed. stored is the entry of field that could not be served, if any: the
// backend is asked whether it is still current (§4.3), and it answers in place
// of a backend error when stale-if-error allows it (RFC 5861 §4).
func (c *cache) forward(w http.ResponseWriter, r *http.Request, key, field string, reqCC cacheControl, status cacheStatus, mayStore bool, stored *entry) {
	if reqCC.onlyIfCached {
		// §5.2.1.7: no stored response may be used. This response is our own
		// and the request is not forwarded: no Cache-Status (RFC 9211 §2).
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	m := &missHook{c: c, req: r, status: status, store: mayStore && r.Method == http.MethodGet, requestTime: time.Now(), stored: stored}
	m.head = mayStore && r.Method == http.MethodHead
	var before http.Header
	if stored != nil {
		// What the header holds now, without what the backend will add to it.
		before = w.Header().Clone()
	}
	// The request goes to the backend as it is, with its context and trace
	// headers, unless a stored response is to be validated: its validators
	// then take the place of the conditions of the client (§4.3.1).
	out, validating := backendRequest(r, stored)
	m.validating = validating
	rec := newRecorder(w, m, c.cfg.maxBodyBytes)
	c.next.ServeHTTP(rec, out)
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	ctx := context.WithoutCancel(r.Context())

	if stored != nil && m.staleIfError {
		// Nothing of the backend's error was sent: the stored response replaces it.
		resetHeader(w.Header(), before)
		status.fwdStatus = rec.status
		status.served = true
		status.detail = "stale-if-error"
		c.serve(w, r, stored, m.age, m.lifetime, status)
		return
	}
	if m.validated != nil {
		// Nothing of the backend's 304 was sent: the stored response it
		// validated is (§4.3.3), with its updated fields if it gets some
		// (§4.3.4), and with what the 304 has for this client only.
		h := w.Header()
		resetHeader(h, before)
		for name, values := range m.own {
			h[name] = values
		}
		e := m.validated
		cc := parseCacheControl(e.header.Values("Cache-Control"))
		age := currentAge(e.header, e.requestTime, e.responseTime, time.Now())
		status.fwdStatus = rec.status
		status.served = true
		c.serve(w, r, e, age, freshnessLifetime(e, cc, c.cfg.defaultTTL), status)
		if m.updated {
			// The client gets the whole response before the Redis writes.
			flush(w)
			c.freshen(ctx, r, key, field, e)
		}
		return
	}
	if m.unusable {
		// The 304 was about another response than the stored one (§4.3.4), which
		// cannot answer: the backend is asked again, without its validators.
		resetHeader(w.Header(), before)
		c.forward(w, r, key, field, reqCC, status, mayStore, nil)
		return
	}
	if m.headHeader != nil {
		rec.Flush()
		c.freshenWithHead(ctx, r, key, field, stored, m)
		return
	}
	c.save(ctx, rec, m, key)
}

// freshenWithHead applies the 200 response to a forwarded HEAD to the stored
// GET response that could have answered it (§4.3.5): stored, the entry of
// field, or the one the cache did not read because the request said no-cache.
// The same response gets its fields; another one makes it stale, which a
// fresh entry is made by deleting it. A response with another content coding
// is another variant: it says nothing of this one. Neither does a response
// without validator: some backends answer a HEAD with next to no header, which
// must not make a stale entry fresh.
func (c *cache) freshenWithHead(ctx context.Context, r *http.Request, key, field string, stored *entry, m *missHook) {
	h := m.headHeader
	if stored == nil {
		if lookupAllowed(r) {
			return // the cache was read: it has no such response
		}
		e, f, _, err := c.find(ctx, key, r.Header)
		if err != nil || e == nil {
			return
		}
		stored = e
		field = f
	}
	if stored.header.Get("Content-Encoding") != h.Get("Content-Encoding") {
		return
	}
	if headMatches(stored, h) {
		if h.Get("ETag") == "" && h.Get("Last-Modified") == "" {
			return
		}
		// Stored again as the response to a GET, which it is.
		get := r.WithContext(ctx)
		get.Method = http.MethodGet
		c.freshen(ctx, get, key, field, refreshed(stored, h, m.requestTime, m.responseTime))
		return
	}
	cc := parseCacheControl(stored.header.Values("Cache-Control"))
	age := currentAge(stored.header, stored.requestTime, stored.responseTime, m.responseTime)
	if age < freshnessLifetime(stored, cc, c.cfg.defaultTTL) {
		_ = c.store.del(ctx, key, field)
	}
}

// resetHeader makes h what it was before the backend wrote to it.
func resetHeader(h, before http.Header) {
	for name := range h {
		delete(h, name)
	}
	for name, values := range before {
		h[name] = values
	}
}

// freshen stores e, the entry of field as a 304 or the response to a HEAD
// refreshed it (§4.3.4, §4.3.5), in its field. One that may no longer be
// stored is deleted instead, unless it is only refused because of the request
// (see supersedes).
func (c *cache) freshen(ctx context.Context, req *http.Request, key, field string, e *entry) {
	ttl, ok := storeTTL(req, e.status, e.header, c.cfg, e.requestTime, e.responseTime)
	if !ok {
		if supersedes(e.status, e.header, c.cfg, e.requestTime, e.responseTime) {
			_ = c.store.del(ctx, key, field)
		}
		return
	}
	data := encodeEntry(e)
	if int64(len(data)) > c.maxEntry {
		return
	}
	if field == "" {
		_ = c.store.set(ctx, key, "", data, ttl)
	} else {
		c.saveVariant(ctx, key, variantOf(req, e, field), data, ttl)
	}
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

	// stored is the entry that could not be served, if any.
	// Validation (§4.3): validating is set when the backend got its
	// validators. Its 304 gives validated, the entry to serve: with the
	// fields of the 304 when updated is set (§4.3.4), as it was otherwise.
	// own holds the fields of the 304 that are for this client only. A 304
	// about another response is unusable.
	stored     *entry
	validating bool
	validated  *entry
	updated    bool
	own        http.Header
	unusable   bool

	// head is set for a HEAD that may update the stored GET response
	// (§4.3.5): headHeader is then the header of its 200 response.
	head       bool
	headHeader http.Header

	// stale-if-error: once staleIfError is set, stored replaces the backend
	// error, with this age and freshness lifetime.
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
	if m.validating && code == http.StatusNotModified {
		current, update := validates(m.stored, h)
		if !current {
			// Not the stored response: in the background, it is deleted.
			m.unusable = true
			m.superseded = m.revalidation
			return withhold
		}
		m.own = ownFields(m.stored, h)
		m.validated = m.stored
		if update {
			m.validated = refreshed(m.stored, h, m.requestTime, m.responseTime)
			m.updated = true
		}
		return withhold
	}
	if m.stored != nil && !m.revalidation && staleIfErrorStatus(code) {
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
	if m.head && code == http.StatusOK {
		m.headHeader = h.Clone()
	}
	m.status.fwdStatus = code
	h.Add("Cache-Status", m.status.String())
	if m.store {
		return keepCopy
	}
	return passOn
}

package traedis

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// backend is a scripted origin that records what it receives.
type backend struct {
	status  int
	header  http.Header
	body    string
	calls   int
	lastReq *http.Request
	writer  http.ResponseWriter
}

func (b *backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.calls++
	b.lastReq = r
	b.writer = w
	for k, v := range b.header {
		w.Header()[k] = append([]string(nil), v...)
	}
	if b.status != 0 {
		w.WriteHeader(b.status)
	}
	_, _ = io.WriteString(w, b.body)
}

func newTestCache(t *testing.T, b *backend, mutate func(*Config)) (*cache, *memStore) {
	t.Helper()
	cfg := CreateConfig()
	if mutate != nil {
		mutate(cfg)
	}
	s, err := parseConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	return newCache(b, "cache", s, st), st
}

func doRequest(h http.Handler, method, target string, hdr http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cacheableBackend() *backend {
	return &backend{
		header: http.Header{"Cache-Control": {"public, max-age=60"}, "Content-Type": {"text/plain"}},
		body:   "hello",
	}
}

// closeTo allows for the backend response delay, counted in the initial age (§4.2.3).
func closeTo(got, want time.Duration) bool {
	return got <= want && got > want-time.Second
}

const testURL = "http://example.com/600x400?b=2&a=1"

func lastCacheStatus(rec *httptest.ResponseRecorder) string {
	values := rec.Header().Values("Cache-Status")
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func TestMissThenHit(t *testing.T) {
	b := cacheableBackend()
	c, st := newTestCache(t, b, nil)

	first := doRequest(c, http.MethodGet, testURL, nil)
	if first.Code != 200 || first.Body.String() != "hello" {
		t.Fatalf("miss: got %d %q", first.Code, first.Body.String())
	}
	if got := lastCacheStatus(first); got != "traedis; fwd=uri-miss; fwd-status=200" {
		t.Errorf("miss Cache-Status = %q", got)
	}
	v, ok := st.lookup(redisKey("http://example.com/600x400?a=1&b=2"), "")
	if !ok {
		t.Fatal("response not stored")
	}
	if want := time.Minute + time.Hour; !closeTo(v.ttl, want) {
		t.Errorf("stored ttl = %v, want %v", v.ttl, want)
	}

	// Same resource with another parameter order.
	second := doRequest(c, http.MethodGet, "http://EXAMPLE.com/600x400?a=1&b=2", nil)
	if b.calls != 1 {
		t.Errorf("backend calls = %d, want 1", b.calls)
	}
	if second.Code != 200 || second.Body.String() != "hello" || second.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("hit: got %d %q %v", second.Code, second.Body.String(), second.Header())
	}
	if got := lastCacheStatus(second); !strings.HasPrefix(got, "traedis; hit; ttl=") {
		t.Errorf("hit Cache-Status = %q", got)
	}
	if second.Header().Get("Age") != "0" || second.Header().Get("Content-Length") != "5" {
		t.Errorf("Age = %q, Content-Length = %q", second.Header().Get("Age"), second.Header().Get("Content-Length"))
	}
	if len(second.Header().Values("Cache-Status")) != 1 {
		t.Errorf("our miss Cache-Status must not be stored: %q", second.Header().Values("Cache-Status"))
	}
}

func storeEntry(t *testing.T, st *memStore, uri string, e *entry) {
	t.Helper()
	if err := st.set(context.Background(), redisKey(uri), "", encodeEntry(e), time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestHitAgeAndTTL(t *testing.T) {
	b := cacheableBackend()
	c, st := newTestCache(t, b, nil)
	stored := time.Now().Add(-10 * time.Second)
	storeEntry(t, st, "http://example.com/a", &entry{
		status:       200,
		header:       http.Header{"Cache-Control": {"max-age=60"}, "Date": {stored.UTC().Format(http.TimeFormat)}},
		body:         []byte("cached"),
		requestTime:  stored,
		responseTime: stored,
	})

	rec := doRequest(c, http.MethodGet, "http://example.com/a", nil)
	if b.calls != 0 || rec.Body.String() != "cached" {
		t.Fatalf("expected a hit, got %q (backend calls %d)", rec.Body.String(), b.calls)
	}
	if age := rec.Header().Get("Age"); age != "10" && age != "11" {
		t.Errorf("§4.2.3 Age = %q, want 10", age)
	}
	if got := lastCacheStatus(rec); got != "traedis; hit; ttl=50" && got != "traedis; hit; ttl=49" {
		t.Errorf("Cache-Status = %q", got)
	}
}

func TestHeadIsServedFromStoredGet(t *testing.T) {
	b := cacheableBackend()
	c, _ := newTestCache(t, b, nil)
	doRequest(c, http.MethodGet, testURL, nil)

	rec := doRequest(c, http.MethodHead, testURL, nil)
	if b.calls != 1 {
		t.Errorf("backend calls = %d, want 1", b.calls)
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "5" {
		t.Errorf("HEAD: body %q, Content-Length %q", rec.Body.String(), rec.Header().Get("Content-Length"))
	}
	if !strings.HasPrefix(lastCacheStatus(rec), "traedis; hit") {
		t.Errorf("Cache-Status = %q", lastCacheStatus(rec))
	}
}

func TestHeadMissIsNotStored(t *testing.T) {
	b := cacheableBackend()
	c, st := newTestCache(t, b, nil)
	rec := doRequest(c, http.MethodHead, testURL, nil)
	if b.calls != 1 || b.lastReq.Method != http.MethodHead {
		t.Errorf("backend must receive the HEAD request")
	}
	if st.sets != 0 {
		t.Error("a HEAD response must not be stored")
	}
	if got := lastCacheStatus(rec); got != "traedis; fwd=uri-miss; fwd-status=200" {
		t.Errorf("Cache-Status = %q", got)
	}
}

func TestRequestDirectives(t *testing.T) {
	tests := []struct {
		name       string
		header     http.Header
		wantCalls  int
		wantCode   int
		wantStatus string
	}{
		{name: "§5.2.1.4 no-cache forwards and refreshes", header: http.Header{"Cache-Control": {"no-cache"}}, wantCalls: 2, wantCode: 200, wantStatus: "traedis; fwd=request; fwd-status=200"},
		{name: "§5.4 Pragma no-cache forwards", header: http.Header{"Pragma": {"no-cache"}}, wantCalls: 2, wantCode: 200, wantStatus: "traedis; fwd=request; fwd-status=200"},
		{name: "§5.2.1.1 max-age=0 forwards", header: http.Header{"Cache-Control": {"max-age=0"}}, wantCalls: 2, wantCode: 200, wantStatus: "traedis; fwd=stale; fwd-status=200"},
		{name: "§5.2.1.3 min-fresh too large forwards", header: http.Header{"Cache-Control": {"min-fresh=3600"}}, wantCalls: 2, wantCode: 200, wantStatus: "traedis; fwd=stale; fwd-status=200"},
		{name: "§5.2.1.5 no-store may be served", header: http.Header{"Cache-Control": {"no-store"}}, wantCalls: 1, wantCode: 200, wantStatus: "hit"},
		{name: "§5.2.1.7 only-if-cached served from cache", header: http.Header{"Cache-Control": {"only-if-cached"}}, wantCalls: 1, wantCode: 200, wantStatus: "hit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := cacheableBackend()
			c, _ := newTestCache(t, b, nil)
			doRequest(c, http.MethodGet, testURL, nil)
			rec := doRequest(c, http.MethodGet, testURL, tt.header)
			if b.calls != tt.wantCalls || rec.Code != tt.wantCode {
				t.Errorf("backend calls = %d, code = %d; want %d, %d", b.calls, rec.Code, tt.wantCalls, tt.wantCode)
			}
			if got := lastCacheStatus(rec); !strings.Contains(got, tt.wantStatus) {
				t.Errorf("Cache-Status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

func TestOnlyIfCachedMissIs504(t *testing.T) {
	b := cacheableBackend()
	c, _ := newTestCache(t, b, nil)
	rec := doRequest(c, http.MethodGet, testURL, http.Header{"Cache-Control": {"only-if-cached"}})
	if rec.Code != http.StatusGatewayTimeout || b.calls != 0 {
		t.Errorf("§5.2.1.7 got %d with %d backend calls, want 504 and none", rec.Code, b.calls)
	}
}

func TestStaleEntryIsRefreshed(t *testing.T) {
	b := cacheableBackend()
	c, st := newTestCache(t, b, nil)
	old := time.Now().Add(-2 * time.Minute)
	storeEntry(t, st, "http://example.com/a", &entry{
		status: 200, header: http.Header{"Cache-Control": {"max-age=60"}}, body: []byte("old"),
		requestTime: old, responseTime: old,
	})
	rec := doRequest(c, http.MethodGet, "http://example.com/a", nil)
	if b.calls != 1 || rec.Body.String() != "hello" {
		t.Errorf("§4.2 a stale entry must not be served (no stale support yet)")
	}
	if got := lastCacheStatus(rec); got != "traedis; fwd=stale; fwd-status=200" {
		t.Errorf("Cache-Status = %q", got)
	}
	if st.sets != 2 {
		t.Errorf("the entry must be overwritten")
	}
}

func TestCorruptEntryIsAMiss(t *testing.T) {
	b := cacheableBackend()
	c, st := newTestCache(t, b, nil)
	if err := st.set(context.Background(), redisKey("http://example.com/a"), "", []byte("TRD1garbage"), time.Hour); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(c, http.MethodGet, "http://example.com/a", nil)
	if b.calls != 1 || rec.Body.String() != "hello" || lastCacheStatus(rec) != "traedis; fwd=uri-miss; fwd-status=200" {
		t.Errorf("a corrupt entry must be a miss: %q %q", rec.Body.String(), lastCacheStatus(rec))
	}
}

func TestFailOpenWhenStoreFails(t *testing.T) {
	b := cacheableBackend()
	c, st := newTestCache(t, b, nil)
	st.err = errors.New("redis down")
	rec := doRequest(c, http.MethodGet, testURL, nil)
	if rec.Code != 200 || rec.Body.String() != "hello" {
		t.Fatalf("fail open: got %d %q", rec.Code, rec.Body.String())
	}
	if got := lastCacheStatus(rec); got != "traedis; fwd=bypass; fwd-status=200; detail=redis" {
		t.Errorf("Cache-Status = %q", got)
	}
}

func TestFailOpenWithUnreachableRedis(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	cfg := CreateConfig()
	cfg.Redis.DSN = "redis://" + addr + "/0"
	b := cacheableBackend()
	h, err := New(context.Background(), b, cfg, "cache")
	if err != nil {
		t.Fatal(err)
	}
	rec := doRequest(h, http.MethodGet, testURL, nil)
	if rec.Code != 200 || rec.Body.String() != "hello" || b.calls != 1 {
		t.Errorf("fail open: got %d %q", rec.Code, rec.Body.String())
	}
}

func TestNotStored(t *testing.T) {
	tests := []struct {
		name    string
		reqHdr  http.Header
		respHdr http.Header
		status  int
		body    string
		mutate  func(*Config)
	}{
		{name: "§5.2.2.5 no-store", respHdr: http.Header{"Cache-Control": {"no-store"}}},
		{name: "§5.2.2.7 private", respHdr: http.Header{"Cache-Control": {"private, max-age=60"}}},
		{name: "§3.5 Authorization without public", reqHdr: http.Header{"Authorization": {"Bearer x"}}, respHdr: http.Header{"Cache-Control": {"max-age=60"}}},
		{name: "Set-Cookie without public", respHdr: http.Header{"Cache-Control": {"max-age=60"}, "Set-Cookie": {"sid=1"}}},
		{name: "Vary not supported yet", respHdr: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"Accept-Encoding"}}},
		{name: "vary option not supported yet", respHdr: http.Header{"Cache-Control": {"max-age=60"}}, mutate: func(c *Config) { c.Vary = []string{"Accept-Language"} }},
		{name: "statusCodes narrows", status: 404, respHdr: http.Header{"Cache-Control": {"max-age=60"}}},
		{name: "defaultTtl not applied to requests with Cookie", reqHdr: http.Header{"Cookie": {"sid=1"}}, respHdr: http.Header{}},
		{name: "body larger than maxBodyBytes is streamed, not stored", respHdr: http.Header{"Cache-Control": {"max-age=60"}}, body: strings.Repeat("x", 100), mutate: func(c *Config) { c.MaxBodyBytes = 10 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			if body == "" {
				body = "hello"
			}
			b := &backend{status: tt.status, header: tt.respHdr, body: body}
			c, st := newTestCache(t, b, tt.mutate)
			rec := doRequest(c, http.MethodGet, testURL, tt.reqHdr)
			if rec.Body.String() != body {
				t.Errorf("client got %d bytes, want %d", rec.Body.Len(), len(body))
			}
			if st.sets != 0 {
				t.Error("response must not be stored")
			}
		})
	}
}

func TestSetCookieIsNotReplayed(t *testing.T) {
	b := &backend{header: http.Header{"Cache-Control": {"public, max-age=60"}, "Set-Cookie": {"sid=1"}}, body: "hello"}
	c, _ := newTestCache(t, b, nil)
	first := doRequest(c, http.MethodGet, testURL, nil)
	if first.Header().Get("Set-Cookie") != "sid=1" {
		t.Error("the client causing the miss must get Set-Cookie")
	}
	second := doRequest(c, http.MethodGet, testURL, nil)
	if b.calls != 1 || second.Header().Get("Set-Cookie") != "" {
		t.Errorf("hit must not replay Set-Cookie (calls %d, Set-Cookie %q)", b.calls, second.Header().Get("Set-Cookie"))
	}
}

func TestDefaultTTLWithoutExplicitFreshness(t *testing.T) {
	b := &backend{body: "hello"}
	c, st := newTestCache(t, b, nil)
	doRequest(c, http.MethodGet, testURL, nil)
	rec := doRequest(c, http.MethodGet, testURL, nil)
	if b.calls != 1 || !strings.HasPrefix(lastCacheStatus(rec), "traedis; hit; ttl=") {
		t.Errorf("§4.2.2 expected a hit with defaultTtl, got %q", lastCacheStatus(rec))
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Error("Cache-Control must never be rewritten")
	}
	if v, _ := st.lookup(redisKey("http://example.com/600x400?a=1&b=2"), ""); !closeTo(v.ttl, 5*time.Minute+time.Hour) {
		t.Errorf("ttl = %v", v.ttl)
	}
}

func TestCacheStatusAppendedAfterUpstream(t *testing.T) {
	b := cacheableBackend()
	b.header["Cache-Status"] = []string{"origin; hit"}
	c, _ := newTestCache(t, b, nil)
	miss := doRequest(c, http.MethodGet, testURL, nil)
	hit := doRequest(c, http.MethodGet, testURL, nil)
	for _, rec := range []*httptest.ResponseRecorder{miss, hit} {
		values := rec.Header().Values("Cache-Status")
		if len(values) != 2 || values[0] != "origin; hit" || !strings.HasPrefix(values[1], "traedis;") {
			t.Errorf("RFC 9211 Cache-Status = %q", values)
		}
	}
}

func TestExposeKey(t *testing.T) {
	b := cacheableBackend()
	c, _ := newTestCache(t, b, func(cfg *Config) { cfg.ExposeKey = true })
	rec := doRequest(c, http.MethodGet, testURL, nil)
	want := `key="` + redisKey("http://example.com/600x400?a=1&b=2") + `"`
	if !strings.Contains(lastCacheStatus(rec), want) {
		t.Errorf("Cache-Status = %q, want it to contain %s", lastCacheStatus(rec), want)
	}

	c, _ = newTestCache(t, b, nil)
	if rec := doRequest(c, http.MethodGet, testURL, nil); strings.Contains(lastCacheStatus(rec), "key=") {
		t.Error("the key must not be exposed by default")
	}
}

func TestPassThroughDoesNotWrapWriter(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		header     http.Header
		wantStatus string
	}{
		{name: "§4 unsafe method", method: http.MethodPost, wantStatus: "traedis; fwd=method"},
		{name: "WebSocket upgrade", method: http.MethodGet, header: http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}}, wantStatus: "traedis; fwd=bypass"},
		{name: "server-sent events", method: http.MethodGet, header: http.Header{"Accept": {"text/event-stream"}}, wantStatus: "traedis; fwd=bypass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := cacheableBackend()
			c, st := newTestCache(t, b, nil)
			rec := doRequest(c, tt.method, testURL, tt.header)
			if _, ok := b.writer.(*httptest.ResponseRecorder); !ok {
				t.Error("the backend must get the original writer (http.Flusher preserved)")
			}
			if st.sets != 0 || b.calls != 1 {
				t.Errorf("sets = %d, calls = %d", st.sets, b.calls)
			}
			if got := rec.Header().Values("Cache-Status"); len(got) != 1 || got[0] != tt.wantStatus {
				t.Errorf("Cache-Status = %q", got)
			}
		})
	}
}

type ctxKey struct{}

func TestTraceContextIsPropagated(t *testing.T) {
	b := cacheableBackend()
	c, _ := newTestCache(t, b, nil)
	req := httptest.NewRequest(http.MethodGet, testURL, nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, "span"))
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("Tracestate", "vendor=1")
	c.ServeHTTP(httptest.NewRecorder(), req)

	got := b.lastReq
	if got.Context().Value(ctxKey{}) != "span" {
		t.Error("the request context must reach the backend")
	}
	if got.Header.Get("Traceparent") != req.Header.Get("Traceparent") || got.Header.Get("Tracestate") != "vendor=1" {
		t.Error("traceparent/tracestate must reach the backend")
	}
}

package traedis

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Background revalidations (RFC 5861 §3) are bounded: at most maxRevalidations
// at once, one per key, each given revalidationTimeout.
const (
	maxRevalidations    = 10
	revalidationTimeout = 30 * time.Second
)

// revalidate refreshes the entry of key in the background (RFC 5861 §3), unless
// it is already being refreshed or maxRevalidations are running: the next
// request for a stale entry tries again.
func (c *cache) revalidate(r *http.Request, key string) {
	c.revalMu.Lock()
	busy := c.revalidating[key] || len(c.revalidating) >= maxRevalidations
	if !busy {
		c.revalidating[key] = true
	}
	c.revalMu.Unlock()
	if busy {
		return
	}

	// The cache's own request for the whole representation, cloned while r is
	// still ours. It keeps the trace headers but not the request context:
	// Traefik keeps there the state of a request (access log fields…) that
	// must not be written to once its response is sent.
	req := r.Clone(context.Background())
	req.Method = http.MethodGet
	req.Body = http.NoBody
	req.ContentLength = 0
	for _, name := range clientOnly {
		req.Header.Del(name)
	}
	go c.refresh(req, key)
}

// refresh runs a background revalidation: without conditional requests yet
// (§4.3), a plain GET whose response replaces the entry. A response that is
// not stored but supersedes the entry deletes it; a backend error leaves it
// in place, to be served stale for as long as it is allowed.
func (c *cache) refresh(req *http.Request, key string) {
	defer c.revalidated(key)
	ctx, cancel := context.WithTimeout(req.Context(), revalidationTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	m := &missHook{c: c, req: req, store: true, revalidation: true, requestTime: time.Now()}
	rec := newRecorder(&nullWriter{header: http.Header{}}, m, c.cfg.maxBodyBytes)
	c.next.ServeHTTP(rec, req)
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
	if !c.save(ctx, rec, m, key) && m.superseded {
		_ = c.store.del(ctx, key, "")
	}
}

// revalidated ends the revalidation of key. Nothing else recovers a panic of
// the backend in a goroutine of ours: it must not take Traefik down.
func (c *cache) revalidated(key string) {
	if p := recover(); p != nil {
		fmt.Fprintf(os.Stderr, "traedis: panic during a background revalidation: %v\n", p)
	}
	c.revalMu.Lock()
	delete(c.revalidating, key)
	c.revalMu.Unlock()
}

// nullWriter is the client of a background revalidation: nobody.
type nullWriter struct {
	header http.Header
}

func (w *nullWriter) Header() http.Header         { return w.header }
func (w *nullWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *nullWriter) WriteHeader(int)             {}

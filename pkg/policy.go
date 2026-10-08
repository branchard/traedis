package traedis

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// maxDeltaSeconds is the cap for delta-seconds values (RFC 9111 §1.2.2).
const maxDeltaSeconds = 2147483648

// cacheControl holds the Cache-Control directives the cache acts upon.
// Delta-seconds fields are -1 when absent.
type cacheControl struct {
	noStore         bool
	noCache         bool // the qualified form is treated as unqualified (§5.2.2.4)
	private         bool // the qualified form is treated as unqualified (§5.2.2.7)
	public          bool
	mustRevalidate  bool
	proxyRevalidate bool
	onlyIfCached    bool
	maxAge          int64
	sMaxAge         int64
	minFresh        int64
	// RFC 5861 windows. They don't affect freshness: a malformed value is
	// ignored and the first one wins.
	staleWhileRevalidate int64
	staleIfError         int64
	// invalid is set by a malformed or duplicated delta-seconds directive:
	// such a response is considered stale (§4.2.1).
	invalid bool
}

func parseCacheControl(values []string) cacheControl {
	cc := cacheControl{maxAge: -1, sMaxAge: -1, minFresh: -1, staleWhileRevalidate: -1, staleIfError: -1}
	for _, line := range values {
		for _, d := range splitDirectives(line) {
			name, value := d, ""
			if i := strings.IndexByte(d, '='); i >= 0 {
				name = d[:i]
				value = unquote(strings.TrimSpace(d[i+1:]))
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "no-store":
				cc.noStore = true
			case "no-cache":
				cc.noCache = true
			case "private":
				cc.private = true
			case "public":
				cc.public = true
			case "must-revalidate":
				cc.mustRevalidate = true
			case "proxy-revalidate":
				cc.proxyRevalidate = true
			case "only-if-cached":
				cc.onlyIfCached = true
			case "max-age":
				cc.maxAge = cc.seconds(cc.maxAge, value)
			case "s-maxage":
				cc.sMaxAge = cc.seconds(cc.sMaxAge, value)
			case "min-fresh":
				cc.minFresh = cc.seconds(cc.minFresh, value)
			case "stale-while-revalidate":
				cc.staleWhileRevalidate = staleSeconds(cc.staleWhileRevalidate, value)
			case "stale-if-error":
				cc.staleIfError = staleSeconds(cc.staleIfError, value)
			}
		}
	}
	return cc
}

// seconds parses a delta-seconds value. A duplicate or malformed value marks
// the directives invalid and keeps the current value.
func (cc *cacheControl) seconds(current int64, value string) int64 {
	n, ok := parseDeltaSeconds(value)
	if !ok || current >= 0 {
		cc.invalid = true
		return current
	}
	return n
}

// staleSeconds parses the delta-seconds of an RFC 5861 directive, keeping the
// current value when there is one or when the new one is malformed.
func staleSeconds(current int64, value string) int64 {
	n, ok := parseDeltaSeconds(value)
	if !ok || current >= 0 {
		return current
	}
	return n
}

func parseDeltaSeconds(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		if n < maxDeltaSeconds {
			n = n*10 + int64(c-'0')
		}
	}
	if n > maxDeltaSeconds {
		n = maxDeltaSeconds
	}
	return n, true
}

// splitDirectives splits a Cache-Control line on commas outside quoted strings.
func splitDirectives(s string) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			quoted = !quoted
		}
		if s[i] == ',' && !quoted {
			out = appendDirective(out, s[start:i])
			start = i + 1
		}
	}
	return appendDirective(out, s[start:])
}

func appendDirective(out []string, d string) []string {
	if d = strings.TrimSpace(d); d != "" {
		out = append(out, d)
	}
	return out
}

// explicitFreshness returns the freshness lifetime set by the response (§4.2.1):
// s-maxage, then max-age, then Expires minus Date. ok is false when the response
// has no explicit freshness.
func explicitFreshness(h http.Header, cc cacheControl, responseTime time.Time) (time.Duration, bool) {
	if cc.invalid {
		return 0, true
	}
	if cc.sMaxAge >= 0 {
		return seconds(cc.sMaxAge), true
	}
	if cc.maxAge >= 0 {
		return seconds(cc.maxAge), true
	}
	expires := h.Values("Expires")
	if len(expires) == 0 {
		return 0, false
	}
	exp, err := http.ParseTime(expires[0])
	if err != nil || len(expires) > 1 {
		// Invalid or duplicated Expires: already expired (§5.3, §4.2.1).
		return 0, true
	}
	if d := exp.Sub(dateValue(h, responseTime)); d > 0 {
		return d, true
	}
	return 0, true
}

// heuristicallyCacheable lists the status codes that may get heuristic freshness
// (RFC 9111 §4.2.2, RFC 9110 §15.1). defaultTtl is our heuristic.
func heuristicallyCacheable(status int) bool {
	switch status {
	case 200, 203, 204, 206, 300, 301, 308, 404, 405, 410, 414, 501:
		return true
	}
	return false
}

// freshnessLifetime returns the lifetime of a stored entry: explicit freshness,
// or defaultTtl. Entries only get stored when defaultTtl was allowed for them.
// cc is the Cache-Control of the entry.
func freshnessLifetime(e *entry, cc cacheControl, defaultTTL time.Duration) time.Duration {
	if d, ok := explicitFreshness(e.header, cc, e.responseTime); ok {
		return d
	}
	if heuristicallyCacheable(e.status) {
		return defaultTTL
	}
	return 0
}

func dateValue(h http.Header, responseTime time.Time) time.Time {
	if t, err := http.ParseTime(h.Get("Date")); err == nil {
		return t
	}
	return responseTime
}

// currentAge implements RFC 9111 §4.2.3.
func currentAge(h http.Header, requestTime, responseTime, now time.Time) time.Duration {
	apparentAge := responseTime.Sub(dateValue(h, responseTime))
	if apparentAge < 0 {
		apparentAge = 0
	}
	var ageValue time.Duration
	if n, ok := parseDeltaSeconds(strings.TrimSpace(h.Get("Age"))); ok {
		ageValue = seconds(n)
	}
	correctedAgeValue := ageValue + responseTime.Sub(requestTime)
	correctedInitialAge := apparentAge
	if correctedAgeValue > correctedInitialAge {
		correctedInitialAge = correctedAgeValue
	}
	return correctedInitialAge + now.Sub(responseTime)
}

// storeTTL decides whether a response may be stored and returns its Redis TTL:
// remaining freshness + staleTtl. RFC 9111 §3, §3.5 and the shared-cache rules
// of CLAUDE.md apply; statusCodes only narrows what may be stored.
func storeTTL(req *http.Request, status int, h http.Header, cfg settings, requestTime, responseTime time.Time) (time.Duration, bool) {
	if req.Method != http.MethodGet {
		return 0, false
	}
	if parseCacheControl(req.Header.Values("Cache-Control")).noStore {
		return 0, false
	}
	if !slices.Contains(cfg.statusCodes, status) || status < 200 || status == http.StatusPartialContent ||
		status == http.StatusNotModified || h.Get("Content-Range") != "" {
		return 0, false
	}

	cc := parseCacheControl(h.Values("Cache-Control"))
	if cc.noStore || cc.private {
		return 0, false
	}
	// No revalidation yet: a no-cache response could never be served.
	if cc.noCache {
		return 0, false
	}
	hasAuth := req.Header.Get("Authorization") != ""
	if hasAuth && !cc.public && cc.sMaxAge < 0 && !cc.mustRevalidate {
		return 0, false
	}
	hasSetCookie := len(h.Values("Set-Cookie")) > 0
	if hasSetCookie && !cc.public {
		return 0, false
	}
	// §4.1: a response is stored as the variant its Vary selects, if it has one.
	if !storableVary(h, cfg.maxVariants) {
		return 0, false
	}
	// Trailers are not part of stored entries.
	if hasHeaderValue(h, "Trailer") || hasTrailerPrefix(h) {
		return 0, false
	}

	lifetime, explicit := explicitFreshness(h, cc, responseTime)
	if !explicit {
		// defaultTtl is heuristic freshness: never for personalized exchanges.
		if !heuristicallyCacheable(status) || cfg.defaultTTL <= 0 ||
			hasAuth || req.Header.Get("Cookie") != "" || hasSetCookie {
			return 0, false
		}
		lifetime = cfg.defaultTTL
	}

	remaining := lifetime - currentAge(h, requestTime, responseTime, responseTime)
	if remaining <= 0 {
		return 0, false
	}
	return remaining + cfg.staleTTL, true
}

// lookupAllowed reports whether the request lets the cache answer from storage
// (§5.2.1.4, §5.4).
func lookupAllowed(req *http.Request) bool {
	values := req.Header.Values("Cache-Control")
	if len(values) > 0 {
		return !parseCacheControl(values).noCache
	}
	for _, p := range req.Header.Values("Pragma") {
		if strings.Contains(strings.ToLower(p), "no-cache") {
			return false
		}
	}
	return true
}

// servable reports whether a stored response with the given age and lifetime is
// fresh and satisfies the request's max-age and min-fresh (§4.2, §5.2.1).
func servable(reqCC cacheControl, age, lifetime time.Duration) bool {
	if age >= lifetime {
		return false
	}
	if reqCC.maxAge >= 0 && age > seconds(reqCC.maxAge) {
		return false
	}
	if reqCC.minFresh >= 0 && lifetime-age < seconds(reqCC.minFresh) {
		return false
	}
	return true
}

// staleAllowed reports whether a stored response may be served stale under an
// RFC 5861 directive, for its delta-seconds past the freshness lifetime. When
// the response does not carry the directive (-1), the configured fallback
// applies instead (§4.2.4: stale responses may also be permitted by
// configuration). Either way the window is capped at staleTtl. staleness is
// negative while the response is fresh. No directive of the response may forbid
// serving stale (§4.2.4); s-maxage implies proxy-revalidate (§5.2.2.10).
func staleAllowed(cc cacheControl, directive int64, fallback, staleness, staleTTL time.Duration) bool {
	if cc.noCache || cc.mustRevalidate || cc.proxyRevalidate || cc.sMaxAge >= 0 {
		return false
	}
	window := fallback
	if directive >= 0 {
		window = seconds(directive)
	}
	if window > staleTTL {
		window = staleTTL
	}
	return window > 0 && staleness < window
}

// servableWhileRevalidating reports whether a stale response may be served
// while it is revalidated in the background (RFC 5861 §3). A request with
// max-age or min-fresh does not want a stale response (§5.2.1.1, §5.2.1.3).
func servableWhileRevalidating(reqCC, cc cacheControl, staleness time.Duration, cfg settings) bool {
	return staleness >= 0 && reqCC.maxAge < 0 && reqCC.minFresh < 0 &&
		staleAllowed(cc, cc.staleWhileRevalidate, cfg.defaultStaleWhileRevalidate, staleness, cfg.staleTTL)
}

// servableOnError reports whether a stored response may replace a backend
// response with the given status (RFC 5861 §4). The request directives are not
// looked at: it applies "regardless of other freshness information", and to a
// response that is still fresh but too old for the client.
func servableOnError(status int, cc cacheControl, staleness time.Duration, cfg settings) bool {
	return staleIfErrorStatus(status) &&
		staleAllowed(cc, cc.staleIfError, cfg.defaultStaleIfError, staleness, cfg.staleTTL)
}

// staleIfErrorStatus lists the errors of RFC 5861 §4.
func staleIfErrorStatus(status int) bool {
	switch status {
	case 500, 502, 503, 504:
		return true
	}
	return false
}

// supersedes reports whether a response to a background revalidation that is
// not stored replaces the stored one anyway, which must then be deleted. It
// does not when the backend is failing or throttling (5xx, 429), nor when the
// response is only refused because of the Authorization or Cookie of the
// request that triggered the revalidation: a request without them would get it
// stored, and clients must not be able to evict entries.
func supersedes(status int, h http.Header, cfg settings, requestTime, responseTime time.Time) bool {
	if status >= 500 || status == http.StatusTooManyRequests {
		return false
	}
	anonymous := &http.Request{Method: http.MethodGet, Header: http.Header{}}
	_, ok := storeTTL(anonymous, status, h, cfg, requestTime, responseTime)
	return !ok
}

// clientOnly lists the request header fields that only concern the
// client's own copy of the response: a background revalidation fetches the
// whole representation, whatever the client asked for.
var clientOnly = []string{
	"Cache-Control", "Pragma", "Range", "If-Range",
	"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since",
}

// hopByHop lists header fields never stored nor replayed (RFC 9110 §7.6.1).
// Content-Length is recomputed when serving.
var hopByHop = []string{
	"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
}

// headerToStore copies the response header without hop-by-hop fields and without
// Set-Cookie: only the client that caused the miss gets the cookie.
func headerToStore(h http.Header) http.Header {
	out := h.Clone()
	for _, v := range h.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out.Del(name)
			}
		}
	}
	for _, name := range hopByHop {
		out.Del(name)
	}
	out.Del("Set-Cookie")
	return out
}

func hasHeaderValue(h http.Header, name string) bool {
	for _, v := range h.Values(name) {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func hasTrailerPrefix(h http.Header) bool {
	for name := range h {
		if strings.HasPrefix(name, http.TrailerPrefix) {
			return true
		}
	}
	return false
}

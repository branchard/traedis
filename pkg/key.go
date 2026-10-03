package traedis

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
)

const keyPrefix = "traedis:"

// cacheURI returns the normalized URI identifying a resource: scheme, lowercased
// host (port kept), escaped path and raw query sorted by parameter name. The
// method is not part of it. This format is stable: changing it invalidates the cache.
func cacheURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}

	uri := scheme + "://" + strings.ToLower(host) + path
	if q := sortedQuery(r.URL.RawQuery); q != "" {
		uri += "?" + q
	}
	return uri
}

// sortedQuery sorts raw query parameters by name, without decoding them. The sort
// is stable so repeated names keep their relative order.
func sortedQuery(raw string) string {
	if raw == "" {
		return ""
	}
	var params []string
	for _, p := range strings.Split(raw, "&") {
		if p != "" {
			params = append(params, p)
		}
	}
	sort.SliceStable(params, func(i, j int) bool {
		return paramName(params[i]) < paramName(params[j])
	})
	return strings.Join(params, "&")
}

func paramName(p string) string {
	if i := strings.IndexByte(p, '='); i >= 0 {
		return p[:i]
	}
	return p
}

// redisKey returns the Redis key of a URI. This format is stable: changing it
// invalidates the cache.
func redisKey(uri string) string {
	sum := sha256.Sum256([]byte(uri))
	return keyPrefix + hex.EncodeToString(sum[:16])
}

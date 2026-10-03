package traedis

import (
	"strconv"
	"time"
)

const cacheName = "traedis"

// cacheStatus is our member of the Cache-Status response header (RFC 9211).
type cacheStatus struct {
	hit       bool
	ttl       time.Duration // remaining freshness, on a hit
	fwd       string        // uri-miss, stale, request, method or bypass
	fwdStatus int           // status received from the backend
	key       string        // only when exposeKey is set
	detail    string        // token
}

func (s cacheStatus) String() string {
	v := cacheName
	if s.hit {
		v += "; hit; ttl=" + strconv.FormatInt(int64(s.ttl/time.Second), 10)
	} else {
		v += "; fwd=" + s.fwd
		if s.fwdStatus > 0 {
			v += "; fwd-status=" + strconv.Itoa(s.fwdStatus)
		}
	}
	if s.key != "" {
		v += "; key=" + strconv.Quote(s.key)
	}
	if s.detail != "" {
		v += "; detail=" + s.detail
	}
	return v
}

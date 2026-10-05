package traedis

import "time"

// unquote strips the double quotes around a quoted-string (RFC 9110 §5.6.4).
// Any other value is returned unchanged; escaped characters are not decoded.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// seconds converts a delta-seconds value (RFC 9111 §1.2.2) to a duration.
func seconds(n int64) time.Duration {
	return time.Duration(n) * time.Second
}

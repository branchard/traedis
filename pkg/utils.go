package traedis

import "time"

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func seconds(n int64) time.Duration {
	return time.Duration(n) * time.Second
}

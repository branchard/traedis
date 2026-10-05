package traedis

import (
	"testing"
	"time"
)

func TestUnquote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "quoted-string is unquoted", in: `"abc"`, want: "abc"},
		{name: "token is kept", in: "abc", want: "abc"},
		{name: "empty quoted-string", in: `""`, want: ""},
		{name: "empty value", in: "", want: ""},
		{name: "lone quote is kept", in: `"`, want: `"`},
		{name: "missing closing quote is kept", in: `"abc`, want: `"abc`},
		{name: "missing opening quote is kept", in: `abc"`, want: `abc"`},
		{name: "only the outer quotes are removed", in: `""abc""`, want: `"abc"`},
		{name: "inner spaces are kept", in: `" a b "`, want: " a b "},
		{name: "escaped characters are not decoded", in: `"a\"b"`, want: `a\"b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unquote(tt.in); got != tt.want {
				t.Errorf("unquote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSeconds(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want time.Duration
	}{
		{name: "zero", in: 0, want: 0},
		{name: "one second", in: 1, want: time.Second},
		{name: "one hour", in: 3600, want: time.Hour},
		{name: "one year", in: 31536000, want: 365 * 24 * time.Hour},
		{name: "negative value", in: -1, want: -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := seconds(tt.in); got != tt.want {
				t.Errorf("seconds(%d) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

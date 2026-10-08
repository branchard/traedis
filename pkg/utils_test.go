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

// The format is stable: Redis keys and variant fields are made of it.
func TestHashHex(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty string", in: "", want: "e3b0c44298fc1c149afbf4c8996fb924"},
		{name: "first 16 bytes of the SHA-256", in: "abc", want: "ba7816bf8f01cfea414140de5dae2223"},
		{name: "a URI", in: "http://example.com/600x400?a=1&b=2", want: "20bc1ca60a67f4c8d352ef97fb644ab7"},
		{name: "bytes, not characters", in: "é", want: "4a99557e4033c3539de2eb65472017ca"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hashHex(tt.in); got != tt.want {
				t.Errorf("hashHex(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	if hashHex("abc") == hashHex("ABC") {
		t.Error("the input case must matter")
	}
}

func TestSameStrings(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{name: "same strings", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "both nil", want: true},
		{name: "nil and empty", a: nil, b: []string{}, want: true},
		{name: "empty strings", a: []string{""}, b: []string{""}, want: true},
		{name: "another order", a: []string{"a", "b"}, b: []string{"b", "a"}},
		{name: "another value", a: []string{"a", "b"}, b: []string{"a", "c"}},
		{name: "another case", a: []string{"a"}, b: []string{"A"}},
		{name: "one more string", a: []string{"a"}, b: []string{"a", "b"}},
		{name: "one less string", a: []string{"a", "b"}, b: []string{"a"}},
		{name: "empty string and nothing", a: []string{""}, b: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameStrings(tt.a, tt.b); got != tt.want {
				t.Errorf("sameStrings(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

package traedis

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// echoKey, as a reply, answers a command with its key as a bulk string.
const echoKey = "echo-key"

// fakeRedis is a scripted RESP server: replies maps a command name to its raw
// reply (defaultReplies otherwise); an empty reply means never answering.
type fakeRedis struct {
	ln       net.Listener
	replies  map[string]string
	mu       sync.Mutex
	commands [][]string
	accepts  int
	conns    []net.Conn
}

var defaultReplies = map[string]string{
	"HGET":   "$5\r\nhello\r\n",
	"HSETEX": ":1\r\n",
	"AUTH":   "+OK\r\n",
	"SELECT": "+OK\r\n",
}

func newFakeRedis(t *testing.T, replies map[string]string) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{}
	for k, v := range defaultReplies {
		all[k] = v
	}
	for k, v := range replies {
		all[k] = v
	}
	f := &fakeRedis{ln: ln, replies: all}
	go f.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.conns {
			_ = c.Close()
		}
	})
	return f
}

func (f *fakeRedis) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.accepts++
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		go f.handle(c)
	}
}

func (f *fakeRedis) handle(c net.Conn) {
	r := bufio.NewReader(c)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.commands = append(f.commands, args)
		f.mu.Unlock()
		resp := f.replies[args[0]]
		if resp == "" {
			continue // never answer
		}
		if resp == echoKey {
			resp = "$" + strconv.Itoa(len(args[1])) + "\r\n" + args[1] + "\r\n"
		}
		if _, err := io.WriteString(c, resp); err != nil {
			return
		}
	}
}

func (f *fakeRedis) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepts
}

func (f *fakeRedis) received() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.commands...)
}

func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func testClient(f *fakeRedis) *redisClient {
	return newRedisClient(redisOptions{addr: f.ln.Addr().String(), timeout: 200 * time.Millisecond}, 1024)
}

func TestRedisGetAndSet(t *testing.T) {
	f := newFakeRedis(t, nil)
	c := testClient(f)
	ctx := context.Background()

	v, err := c.get(ctx, "traedis:k", "")
	if err != nil || string(v) != "hello" {
		t.Fatalf("get() = %q, %v", v, err)
	}
	if err := c.set(ctx, "traedis:k", "", []byte("v\r\n1"), 1500*time.Millisecond); err != nil {
		t.Fatalf("set() = %v", err)
	}

	accepts := f.connections()
	cmds := f.received()
	want := []string{
		"HGET traedis:k ",
		"HSETEX traedis:k PX 1500 FIELDS 1  v\r\n1",
	}
	if len(cmds) != len(want) {
		t.Fatalf("commands = %q", cmds)
	}
	for i, w := range want {
		if got := strings.Join(cmds[i], " "); got != w {
			t.Errorf("command %d = %q, want %q", i, got, w)
		}
	}
	if accepts != 1 {
		t.Errorf("connections = %d, want 1 (pooled)", accepts)
	}
}

func TestRedisGetNullIsMiss(t *testing.T) {
	f := newFakeRedis(t, map[string]string{"HGET": "$-1\r\n"})
	if _, err := testClient(f).get(context.Background(), "k", ""); !errors.Is(err, errMiss) {
		t.Fatalf("get() error = %v, want errMiss", err)
	}
}

func TestRedisDialSendsAuthAndSelect(t *testing.T) {
	tests := []struct {
		name string
		opts redisOptions
		want []string
	}{
		{name: "ACL user and database", opts: redisOptions{username: "u", password: "p", db: 15}, want: []string{"AUTH u p", "SELECT 15", "HGET k "}},
		{name: "password only", opts: redisOptions{password: "p"}, want: []string{"AUTH p", "HGET k "}},
		{name: "no credentials, database 0", opts: redisOptions{}, want: []string{"HGET k "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRedis(t, nil)
			opts := tt.opts
			opts.addr = f.ln.Addr().String()
			opts.timeout = 200 * time.Millisecond
			if _, err := newRedisClient(opts, 1024).get(context.Background(), "k", ""); err != nil {
				t.Fatal(err)
			}
			cmds := f.received()
			var got []string
			for _, c := range cmds {
				got = append(got, strings.Join(c, " "))
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("commands = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedisAuthFailure(t *testing.T) {
	f := newFakeRedis(t, map[string]string{"AUTH": "-WRONGPASS invalid username-password pair\r\n"})
	opts := redisOptions{addr: f.ln.Addr().String(), password: "s3cret", timeout: 200 * time.Millisecond}
	_, err := newRedisClient(opts, 1024).get(context.Background(), "k", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestRedisErrorReplyKeepsConnection(t *testing.T) {
	f := newFakeRedis(t, map[string]string{"HSETEX": "-ERR unknown command 'HSETEX'\r\n"})
	c := testClient(f)
	ctx := context.Background()
	if err := c.set(ctx, "k", "", []byte("v"), time.Second); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("set() error = %v, want the server error", err)
	}
	if _, err := c.get(ctx, "k", ""); err != nil {
		t.Fatal(err)
	}
	if accepts := f.connections(); accepts != 1 {
		t.Errorf("connections = %d, want 1", accepts)
	}
}

func TestRedisUntrustedReplies(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		wantErr error
	}{
		{name: "unknown reply type", reply: "?what\r\n", wantErr: errProtocol},
		{name: "missing CR", reply: "$5\nhello\n", wantErr: errProtocol},
		{name: "oversized bulk is a miss without allocating", reply: "$999999999999\r\n", wantErr: errMiss},
		{name: "negative bulk length", reply: "$-7\r\n", wantErr: errProtocol},
		{name: "bulk without trailing CRLF", reply: "$2\r\nhello\r\n", wantErr: errProtocol},
		{name: "status line too long", reply: "+" + strings.Repeat("a", 2*maxLineBytes) + "\r\n", wantErr: errProtocol},
		{name: "integer instead of bulk", reply: ":1\r\n", wantErr: errProtocol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRedis(t, map[string]string{"HGET": tt.reply})
			c := testClient(f)
			if _, err := c.get(context.Background(), "k", ""); !errors.Is(err, tt.wantErr) {
				t.Fatalf("first get() error = %v, want %v", err, tt.wantErr)
			}
			_, _ = c.get(context.Background(), "k", "")
			if tt.wantErr != errProtocol || tt.name == "integer instead of bulk" {
				return
			}
			// After a protocol error the connection is not reused.
			if accepts := f.connections(); accepts != 2 {
				t.Errorf("connections = %d, want 2", accepts)
			}
		})
	}
}

func TestRedisTimeout(t *testing.T) {
	f := newFakeRedis(t, map[string]string{"HGET": ""})
	c := newRedisClient(redisOptions{addr: f.ln.Addr().String(), timeout: 50 * time.Millisecond}, 1024)
	start := time.Now()
	if _, err := c.get(context.Background(), "k", ""); err == nil {
		t.Fatal("expected a timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("get() took %v, want about redis.timeout", elapsed)
	}
}

func TestRedisPoolExhausted(t *testing.T) {
	f := newFakeRedis(t, nil)
	c := newRedisClient(redisOptions{addr: f.ln.Addr().String(), timeout: 50 * time.Millisecond}, 1024)
	for i := 0; i < redisPoolSize; i++ {
		c.sem <- struct{}{}
	}
	if _, err := c.get(context.Background(), "k", ""); err == nil {
		t.Fatal("expected an error when the pool is exhausted")
	}
	if accepts := f.connections(); accepts != 0 {
		t.Errorf("connections = %d, want 0", accepts)
	}
}

func TestRedisPoolWaitsForAConnection(t *testing.T) {
	f := newFakeRedis(t, nil)
	c := newRedisClient(redisOptions{addr: f.ln.Addr().String(), timeout: 2 * time.Second}, 1024)
	for i := 0; i < redisPoolSize; i++ {
		c.sem <- struct{}{}
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		<-c.sem
	}()
	start := time.Now()
	if v, err := c.get(context.Background(), "k", ""); err != nil || string(v) != "hello" {
		t.Fatalf("get() = %q, %v, want the value once a connection is free", v, err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("get() took %v: it did not wait for the pool", elapsed)
	}
}

// Regression test, failing under `yaegi test` only: Yaegi v0.16.1 shares the
// operands of a select statement between the goroutines running it, so the pool
// handed one connection to two requests, which read each other's replies.
func TestRedisPoolConcurrency(t *testing.T) {
	const workers, rounds = 32, 100
	f := newFakeRedis(t, map[string]string{"HGET": echoKey})
	c := newRedisClient(redisOptions{addr: f.ln.Addr().String(), timeout: 5 * time.Second}, 1024)

	failures := make(chan string, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				v, err := c.get(context.Background(), key, "")
				if err != nil || string(v) != key {
					failures <- "get(" + key + ") = " + strconv.Quote(string(v)) + ", " + fmt.Sprint(err)
					return
				}
			}
		}("key-" + strconv.Itoa(w))
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if accepts := f.connections(); accepts > redisPoolSize {
		t.Errorf("connections = %d, want at most %d", accepts, redisPoolSize)
	}
}

func TestRedisUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := newRedisClient(redisOptions{addr: addr, timeout: 50 * time.Millisecond}, 1024)
	if _, err := c.get(context.Background(), "k", ""); err == nil {
		t.Fatal("expected an error")
	}
	if len(c.sem) != 0 {
		t.Errorf("pool tokens leaked: %d", len(c.sem))
	}
}

// Integration tests: only with TRAEDIS_REDIS_DSN (Redis >= 8), on keys they create.
func integrationClient(t *testing.T) *redisClient {
	t.Helper()
	dsn := os.Getenv("TRAEDIS_REDIS_DSN")
	if dsn == "" {
		t.Skip("TRAEDIS_REDIS_DSN not set")
	}
	opts, err := parseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	opts.timeout = time.Second
	return newRedisClient(opts, 1<<20)
}

func testKey(t *testing.T) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return keyPrefix + "test:" + hex.EncodeToString(b)
}

func TestRedisIntegrationRoundTripAndExpiry(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()
	key := testKey(t)

	if _, err := c.get(ctx, key, ""); !errors.Is(err, errMiss) {
		t.Fatalf("get() on a new key = %v, want errMiss", err)
	}
	value := []byte("binary\x00\r\nvalue")
	if err := c.set(ctx, key, "", value, 300*time.Millisecond); err != nil {
		t.Fatalf("set() = %v", err)
	}
	got, err := c.get(ctx, key, "")
	if err != nil || string(got) != string(value) {
		t.Fatalf("get() = %q, %v", got, err)
	}

	time.Sleep(500 * time.Millisecond)
	if _, err := c.get(ctx, key, ""); !errors.Is(err, errMiss) {
		t.Fatalf("get() after expiry = %v, want errMiss", err)
	}
}

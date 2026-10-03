package traedis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	redisPoolSize = 10
	// maxLineBytes bounds RESP status lines; longer lines are a protocol error.
	maxLineBytes = 4096
	logInterval  = time.Minute
)

var (
	errProtocol = errors.New("redis: protocol error")
	errTooLarge = errors.New("redis: value too large")
)

// redisError is an error reply sent by the server.
type redisError string

func (e redisError) Error() string { return "redis: " + string(e) }

// redisClient is a minimal RESP2 client with a bounded connection pool.
// Every operation is bounded by the configured timeout.
type redisClient struct {
	opts     redisOptions
	maxBulk  int64
	sem      chan struct{}   // one token per open connection
	idle     chan *redisConn // idle connections
	logMu    sync.Mutex
	lastLog  time.Time
	logCount int
}

type redisConn struct {
	nc net.Conn
	r  *bufio.Reader
}

type reply struct {
	kind byte // '+', '-', ':', '$'
	str  string
	num  int64
	bulk []byte
	null bool
}

func newRedisClient(opts redisOptions, maxBulk int64) *redisClient {
	return &redisClient{
		opts:    opts,
		maxBulk: maxBulk,
		sem:     make(chan struct{}, redisPoolSize),
		idle:    make(chan *redisConn, redisPoolSize),
	}
}

func (c *redisClient) get(ctx context.Context, key, field string) ([]byte, error) {
	r, err := c.do(ctx, []byte("HGET"), []byte(key), []byte(field))
	if errors.Is(err, errTooLarge) {
		return nil, errMiss
	}
	if err != nil {
		return nil, err
	}
	if r.null {
		return nil, errMiss
	}
	if r.kind != '$' {
		return nil, errProtocol
	}
	return r.bulk, nil
}

func (c *redisClient) set(ctx context.Context, key, field string, value []byte, ttl time.Duration) error {
	ms := ttl.Milliseconds()
	if ms <= 0 {
		return nil
	}
	r, err := c.do(ctx, []byte("HSETEX"), []byte(key), []byte("PX"), []byte(strconv.FormatInt(ms, 10)),
		[]byte("FIELDS"), []byte("1"), []byte(field), value)
	if err != nil {
		return err
	}
	if r.kind != ':' {
		return errProtocol
	}
	return nil
}

// do runs one command. Any failure but an error reply closes the connection:
// its state is unknown.
func (c *redisClient) do(ctx context.Context, args ...[]byte) (reply, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.timeout)
	defer cancel()

	conn, err := c.acquire(ctx)
	if err != nil {
		c.logError(err)
		return reply{}, err
	}
	r, err := conn.roundTrip(ctx, c.maxBulk, args)
	if err != nil {
		c.release(nil)
		_ = conn.nc.Close()
		if !errors.Is(err, errTooLarge) {
			c.logError(err)
		}
		return reply{}, err
	}
	c.release(conn)
	if r.kind == '-' {
		err := redisError(r.str)
		c.logError(err)
		return reply{}, err
	}
	return r, nil
}

func (c *redisClient) acquire(ctx context.Context) (*redisConn, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("redis: pool exhausted: %w", ctx.Err())
	}
	select {
	case conn := <-c.idle:
		return conn, nil
	default:
	}
	conn, err := c.dial(ctx)
	if err != nil {
		<-c.sem
		return nil, err
	}
	return conn, nil
}

// release returns a healthy connection to the pool (nil for a closed one).
func (c *redisClient) release(conn *redisConn) {
	if conn != nil {
		select {
		case c.idle <- conn:
		default:
			_ = conn.nc.Close()
		}
	}
	<-c.sem
}

func (c *redisClient) dial(ctx context.Context) (*redisConn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", c.opts.addr)
	if err != nil {
		return nil, err
	}
	conn := &redisConn{nc: nc, r: bufio.NewReaderSize(nc, maxLineBytes)}

	var setup [][][]byte
	if c.opts.password != "" {
		if c.opts.username != "" {
			setup = append(setup, [][]byte{[]byte("AUTH"), []byte(c.opts.username), []byte(c.opts.password)})
		} else {
			setup = append(setup, [][]byte{[]byte("AUTH"), []byte(c.opts.password)})
		}
	}
	if c.opts.db != 0 {
		setup = append(setup, [][]byte{[]byte("SELECT"), []byte(strconv.Itoa(c.opts.db))})
	}
	for _, args := range setup {
		r, err := conn.roundTrip(ctx, c.maxBulk, args)
		if err == nil && r.kind == '-' {
			err = redisError(r.str)
		}
		if err != nil {
			_ = nc.Close()
			return nil, fmt.Errorf("redis: %s: %w", args[0], err)
		}
	}
	return conn, nil
}

func (conn *redisConn) roundTrip(ctx context.Context, maxBulk int64, args [][]byte) (reply, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.nc.SetDeadline(deadline); err != nil {
			return reply{}, err
		}
	}
	if _, err := conn.nc.Write(encodeCommand(args)); err != nil {
		return reply{}, err
	}
	return readReply(conn.r, maxBulk)
}

func encodeCommand(args [][]byte) []byte {
	size := 16
	for _, a := range args {
		size += len(a) + 16
	}
	b := make([]byte, 0, size)
	b = append(b, '*')
	b = strconv.AppendInt(b, int64(len(args)), 10)
	b = append(b, '\r', '\n')
	for _, a := range args {
		b = append(b, '$')
		b = strconv.AppendInt(b, int64(len(a)), 10)
		b = append(b, '\r', '\n')
		b = append(b, a...)
		b = append(b, '\r', '\n')
	}
	return b
}

// readReply reads one RESP2 reply. The server is not trusted: line lengths are
// bounded and bulk lengths are checked against maxBulk before allocating.
func readReply(r *bufio.Reader, maxBulk int64) (reply, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return reply{}, errProtocol
		}
		return reply{}, err
	}
	if len(line) < 3 || line[len(line)-2] != '\r' {
		return reply{}, errProtocol
	}
	kind := line[0]
	payload := string(line[1 : len(line)-2])

	switch kind {
	case '+', '-':
		return reply{kind: kind, str: payload}, nil
	case ':':
		n, err := strconv.ParseInt(payload, 10, 64)
		if err != nil {
			return reply{}, errProtocol
		}
		return reply{kind: kind, num: n}, nil
	case '$':
		n, err := strconv.ParseInt(payload, 10, 64)
		if err != nil || n < -1 {
			return reply{}, errProtocol
		}
		if n == -1 {
			return reply{kind: kind, null: true}, nil
		}
		if n > maxBulk {
			return reply{}, errTooLarge
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return reply{}, err
		}
		if buf[n] != '\r' || buf[n+1] != '\n' {
			return reply{}, errProtocol
		}
		return reply{kind: kind, bulk: buf[:n]}, nil
	}
	return reply{}, errProtocol
}

// logError reports Redis failures at most once per logInterval, with the number
// of errors since the last report. It never logs the DSN.
func (c *redisClient) logError(err error) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	c.logCount++
	now := time.Now()
	if now.Sub(c.lastLog) < logInterval {
		return
	}
	fmt.Fprintf(os.Stderr, "traedis: redis error, bypassing the cache (%d errors since last report): %v\n", c.logCount, err)
	c.lastLog = now
	c.logCount = 0
}

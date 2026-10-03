package traedis

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"strconv"
)

// headerHook is told about the final status before it is sent downstream.
type headerHook interface {
	// beforeHeader may edit h and returns whether to keep a copy of the body.
	beforeHeader(status int, h http.Header) bool
}

// recorder streams a response to the client while keeping a copy of at most max
// body bytes. It never alters the stream: past max, it stops buffering. It
// preserves http.Flusher, http.Hijacker and trailers (Header is the underlying map).
type recorder struct {
	rw          http.ResponseWriter
	hook        headerHook
	max         int64
	status      int
	wroteHeader bool
	capture     bool
	hijacked    bool
	buf         bytes.Buffer
}

func newRecorder(rw http.ResponseWriter, hook headerHook, max int64) *recorder {
	return &recorder{rw: rw, hook: hook, max: max}
}

func (r *recorder) Header() http.Header {
	return r.rw.Header()
}

func (r *recorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	// Informational responses (103 Early Hints…) pass through; 101 is final.
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		r.rw.WriteHeader(code)
		return
	}
	r.wroteHeader = true
	r.status = code
	r.capture = r.hook.beforeHeader(code, r.rw.Header())
	if cl := r.rw.Header().Get("Content-Length"); r.capture && cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err != nil || n > r.max {
			r.capture = false
		}
	}
	r.rw.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.rw.Write(p)
	if r.capture {
		if err != nil || int64(r.buf.Len()+n) > r.max {
			r.stopCapture()
		} else {
			r.buf.Write(p[:n])
		}
	}
	return n, err
}

func (r *recorder) stopCapture() {
	r.capture = false
	r.buf = bytes.Buffer{}
}

// Flush implements http.Flusher. Yaegi v0.16.1 hides it from compiled code
// (only ResponseWriter+Hijacker is wrapped): the middleware does not wrap the
// writer for responses expected to stream.
func (r *recorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if f, ok := r.rw.(http.Flusher); ok {
		f.Flush()
		return
	}
	_ = http.NewResponseController(r.rw).Flush()
}

// Hijack implements http.Hijacker (WebSockets). A hijacked response is never stored.
func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.hijacked = true
	r.stopCapture()
	if h, ok := r.rw.(http.Hijacker); ok {
		return h.Hijack()
	}
	return http.NewResponseController(r.rw).Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter {
	return r.rw
}

// body returns the captured body, and false when the response was not fully
// captured (not wanted, too large, write error or hijacked).
func (r *recorder) body() ([]byte, bool) {
	if !r.capture || r.hijacked {
		return nil, false
	}
	return r.buf.Bytes(), true
}

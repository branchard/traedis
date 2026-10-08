package traedis

import (
	"encoding/binary"
	"errors"
	"net/http"
	"time"
)

// entryVersion prefixes every stored entry. Bump it on any format change:
// entries with an unknown version are treated as a miss.
const entryVersion = "TRD1"

var errBadEntry = errors.New("invalid cache entry")

// entry is a stored response.
//
// Encoding (big endian):
//
//	"TRD1" | status u16 | requestTime i64 | responseTime i64 (Unix nanoseconds)
//	| header fields | vary fields | body (u32 length + bytes)
//
// where fields = count u32, then for each value: name (u32 length + bytes),
// value (u32 length + bytes).
type entry struct {
	status       int
	header       http.Header
	body         []byte
	requestTime  time.Time
	responseTime time.Time
	// vary holds the request header values selected by the response Vary; for
	// Accept-Encoding, the content codings the request accepted (see vary.go).
	vary http.Header
}

func encodeEntry(e *entry) []byte {
	size := len(entryVersion) + 2 + 8 + 8 + fieldsSize(e.header) + fieldsSize(e.vary) + 4 + len(e.body)
	b := make([]byte, 0, size)
	b = append(b, entryVersion...)
	b = binary.BigEndian.AppendUint16(b, uint16(e.status))
	b = binary.BigEndian.AppendUint64(b, uint64(e.requestTime.UnixNano()))
	b = binary.BigEndian.AppendUint64(b, uint64(e.responseTime.UnixNano()))
	b = appendFields(b, e.header)
	b = appendFields(b, e.vary)
	b = appendBytes(b, e.body)
	return b
}

func fieldsSize(h http.Header) int {
	n := 4
	for name, values := range h {
		n += len(values) * (8 + len(name))
		for _, v := range values {
			n += len(v)
		}
	}
	return n
}

func appendFields(b []byte, h http.Header) []byte {
	count := 0
	for _, values := range h {
		count += len(values)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(count))
	for name, values := range h {
		for _, v := range values {
			b = appendBytes(b, []byte(name))
			b = appendBytes(b, []byte(v))
		}
	}
	return b
}

func appendBytes(b, v []byte) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
	return append(b, v...)
}

// decodeEntry parses an entry. Stored data is untrusted: every length is checked
// against the remaining input before allocating, and any inconsistency is an error.
func decodeEntry(b []byte, maxSize int64) (*entry, error) {
	if int64(len(b)) > maxSize || len(b) < len(entryVersion) || string(b[:len(entryVersion)]) != entryVersion {
		return nil, errBadEntry
	}
	d := &decoder{b: b[len(entryVersion):]}
	e := &entry{}
	e.status = int(d.uint16())
	e.requestTime = time.Unix(0, int64(d.uint64()))
	e.responseTime = time.Unix(0, int64(d.uint64()))
	e.header = d.fields()
	e.vary = d.fields()
	e.body = d.bytes()
	if d.err != nil || len(d.b) != 0 || e.status < 100 || e.status > 599 {
		return nil, errBadEntry
	}
	return e, nil
}

type decoder struct {
	b   []byte
	err error
}

func (d *decoder) take(n int) []byte {
	if d.err != nil || n < 0 || n > len(d.b) {
		d.err = errBadEntry
		return nil
	}
	v := d.b[:n]
	d.b = d.b[n:]
	return v
}

func (d *decoder) uint16() uint16 {
	if v := d.take(2); v != nil {
		return binary.BigEndian.Uint16(v)
	}
	return 0
}

func (d *decoder) uint32() uint32 {
	if v := d.take(4); v != nil {
		return binary.BigEndian.Uint32(v)
	}
	return 0
}

func (d *decoder) uint64() uint64 {
	if v := d.take(8); v != nil {
		return binary.BigEndian.Uint64(v)
	}
	return 0
}

func (d *decoder) bytes() []byte {
	n := d.uint32()
	if uint64(n) > uint64(len(d.b)) {
		d.err = errBadEntry
		return nil
	}
	return d.take(int(n))
}

func (d *decoder) fields() http.Header {
	count := d.uint32()
	// Each value needs at least 8 bytes (two lengths): bound the count before allocating.
	if uint64(count) > uint64(len(d.b))/8 {
		d.err = errBadEntry
		return nil
	}
	h := make(http.Header, count)
	for i := uint32(0); i < count && d.err == nil; i++ {
		name := string(d.bytes())
		value := string(d.bytes())
		h[name] = append(h[name], value)
	}
	return h
}

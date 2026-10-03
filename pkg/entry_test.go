package traedis

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func sampleEntry() *entry {
	return &entry{
		status: 200,
		header: http.Header{
			"Content-Type":  {"image/png"},
			"Cache-Control": {"public, max-age=60"},
			"X-Multi":       {"a", "b"},
		},
		body:         []byte("\x89PNG body"),
		requestTime:  time.Unix(1700000000, 123),
		responseTime: time.Unix(1700000001, 456),
		vary:         http.Header{"Accept-Language": {"fr"}},
	}
}

func TestEntryRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		e    *entry
	}{
		{name: "full entry", e: sampleEntry()},
		{name: "empty body and headers", e: &entry{status: 204, header: http.Header{}, body: []byte{}, vary: http.Header{}, requestTime: time.Unix(0, 0), responseTime: time.Unix(0, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeEntry(encodeEntry(tt.e), 1<<20)
			if err != nil {
				t.Fatalf("decodeEntry() = %v", err)
			}
			if got.status != tt.e.status || !bytes.Equal(got.body, tt.e.body) {
				t.Errorf("status/body = %d %q", got.status, got.body)
			}
			if !got.requestTime.Equal(tt.e.requestTime) || !got.responseTime.Equal(tt.e.responseTime) {
				t.Errorf("times = %v %v", got.requestTime, got.responseTime)
			}
			if !reflect.DeepEqual(got.header, tt.e.header) || !reflect.DeepEqual(got.vary, tt.e.vary) {
				t.Errorf("header = %v, vary = %v", got.header, got.vary)
			}
		})
	}
}

func TestDecodeEntryRejectsInvalidData(t *testing.T) {
	valid := encodeEntry(sampleEntry())

	// Header field count claiming far more values than the input holds.
	hugeCount := append([]byte(entryVersion), make([]byte, 2+8+8)...)
	binary.BigEndian.PutUint16(hugeCount[4:], 200)
	hugeCount = binary.BigEndian.AppendUint32(hugeCount, 0xFFFFFFFF)

	// Body length larger than the remaining input.
	hugeBody := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(hugeBody[len(hugeBody)-len(sampleEntry().body)-4:], 0xFFFFFFF0)

	tests := []struct {
		name    string
		data    []byte
		maxSize int64
	}{
		{name: "empty", data: nil, maxSize: 1 << 20},
		{name: "unknown version is a miss", data: append([]byte("TRD9"), valid[4:]...), maxSize: 1 << 20},
		{name: "oversized entry is a miss", data: valid, maxSize: int64(len(valid) - 1)},
		{name: "trailing garbage", data: append(append([]byte(nil), valid...), 0), maxSize: 1 << 20},
		{name: "lying field count", data: hugeCount, maxSize: 1 << 20},
		{name: "lying body length", data: hugeBody, maxSize: 1 << 20},
		{name: "invalid status", data: func() []byte {
			b := append([]byte(nil), valid...)
			binary.BigEndian.PutUint16(b[4:], 42)
			return b
		}(), maxSize: 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeEntry(tt.data, tt.maxSize); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestDecodeEntryRejectsEveryTruncation(t *testing.T) {
	valid := encodeEntry(sampleEntry())
	for n := 0; n < len(valid); n++ {
		if _, err := decodeEntry(valid[:n], 1<<20); err == nil {
			t.Fatalf("truncated at %d bytes: expected an error", n)
		}
	}
}

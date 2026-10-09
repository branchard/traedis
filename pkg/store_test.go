package traedis

import (
	"context"
	"sync"
	"time"
)

// memStore is the in-memory store used by middleware tests.
type memStore struct {
	mu   sync.Mutex
	data map[string]map[string]memValue
	err  error // returned by every call when set
	// variantErr is returned when reading a field other than "".
	variantErr error
	sets       int
	dels       int
	// invalidations counts the keys removed as a whole.
	invalidations int
}

type memValue struct {
	value []byte
	ttl   time.Duration
}

func newMemStore() *memStore {
	return &memStore{data: map[string]map[string]memValue{}}
}

func (m *memStore) get(_ context.Context, key, field string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	if m.variantErr != nil && field != "" {
		return nil, m.variantErr
	}
	v, ok := m.data[key][field]
	if !ok {
		return nil, errMiss
	}
	return v.value, nil
}

func (m *memStore) set(_ context.Context, key, field string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.data[key] == nil {
		m.data[key] = map[string]memValue{}
	}
	m.data[key][field] = memValue{value: append([]byte(nil), value...), ttl: ttl}
	m.sets++
	return nil
}

func (m *memStore) setVariant(_ context.Context, key, field string, value, marker []byte, ttl time.Duration, replace bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	_, hasMarker := m.data[key][""]
	_, hasField := m.data[key][field]
	if replace && !(hasMarker && hasField) {
		return nil
	}
	if m.data[key] == nil {
		m.data[key] = map[string]memValue{}
	}
	m.data[key][""] = memValue{value: append([]byte(nil), marker...), ttl: ttl}
	m.data[key][field] = memValue{value: append([]byte(nil), value...), ttl: ttl}
	m.sets++
	return nil
}

func (m *memStore) count(_ context.Context, key string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, m.err
	}
	return len(m.data[key]), nil
}

func (m *memStore) del(_ context.Context, key, field string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.data[key], field)
	m.dels++
	return nil
}

func (m *memStore) invalidate(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.data, key)
	m.invalidations++
	return nil
}

func (m *memStore) lookup(key, field string) (memValue, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key][field]
	return v, ok
}

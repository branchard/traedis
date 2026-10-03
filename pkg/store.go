package traedis

import (
	"context"
	"errors"
	"time"
)

// errMiss reports that no usable value is stored.
var errMiss = errors.New("cache miss")

// store keeps encoded entries in one hash per URI key, one field per variant.
type store interface {
	// get returns the value of field in key, or errMiss.
	get(ctx context.Context, key, field string) ([]byte, error)
	// set stores value in field of key, expiring after ttl.
	set(ctx context.Context, key, field string, value []byte, ttl time.Duration) error
}

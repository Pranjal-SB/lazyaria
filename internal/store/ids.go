package store

import "github.com/oklog/ulid/v2"

// NewID returns a fresh sortable job id.
func NewID() string { return ulid.Make().String() }

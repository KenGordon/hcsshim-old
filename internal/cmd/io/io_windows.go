package io

import (
	"context"
	"net/url"
	"time"

	"github.com/pkg/errors"
)

// NewUpstreamIO returns an UpstreamIO instance. Currently we only support named pipes and binary
// logging driver for container IO. When using binary logger `stdout` and `stderr` are assumed to be
// the same and the value of `stderr` is completely ignored.
func NewUpstreamIO(ctx context.Context, id, stdout, stderr, stdin string, terminal bool, ioRetryTimeout time.Duration) (UpstreamIO, error) {
	u, err := url.Parse(stdout)

	// Create IO with named pipes.
	if err != nil || u.Scheme == "" {
		return NewNpipeIO(ctx, stdin, stdout, stderr, terminal, ioRetryTimeout)
	}

	// Create IO for binary logging driver.
	if u.Scheme != "binary" {
		return nil, errors.Errorf("scheme must be 'binary', got: '%s'", u.Scheme)
	}

	return NewBinaryIO(ctx, id, u)
}

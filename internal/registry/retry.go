// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Donald Gifford

package registry

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// retryAttempts is the total try count: one attempt plus three
	// retries.
	retryAttempts = 4
	retryBase     = 250 * time.Millisecond
	retryCap      = 4 * time.Second
)

// withRetry runs op up to retryAttempts times with jittered
// exponential backoff between attempts. Only transient failures
// retry; verification failures and client errors return immediately,
// and context cancellation short-circuits both the sleep and the
// classification.
func withRetry(ctx context.Context, op func() error) error {
	var err error
	for attempt := range retryAttempts {
		if attempt > 0 {
			if serr := sleepBackoff(ctx, attempt); serr != nil {
				return errors.Join(err, serr)
			}
		}
		err = op()
		if err == nil || !retryable(err) {
			return err
		}
	}
	return err
}

// errAttemptTimeout marks an attempt-local document timeout so it
// retries; a caller's own context ending stays permanent.
var errAttemptTimeout = errors.New("attempt timed out")

// retryable classifies err: transport-level failures and 5xx/429
// statuses are worth another attempt; everything else — context
// cancellation, transport-floor refusals, other 4xx (a 404 will
// never heal), and every verification failure (retrying a signature
// check is flapping attacker cover at best) — is permanent.
func retryable(err error) bool {
	if errors.Is(err, errAttemptTimeout) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// A refused redirect surfaces wrapped in *url.Error; classify it
	// before the blanket transport branch — repeating a policy
	// violation cannot heal it.
	if errors.Is(err, errInsecureURL) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= http.StatusInternalServerError || se.code == http.StatusTooManyRequests
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	// A body truncated mid-stream surfaces as unexpected EOF.
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// sleepBackoff waits the jittered backoff for the given attempt (1 =
// first retry) or returns early when the context is done.
func sleepBackoff(ctx context.Context, attempt int) error {
	d := min(retryCap, retryBase<<(attempt-1))
	// Equal jitter: [d/2, d). Spreads herds without ever collapsing
	// the sleep to zero. Not cryptography.
	d = d/2 + rand.N(d/2) //nolint:gosec // non-crypto jitter
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

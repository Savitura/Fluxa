package stellar

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stellar/go/clients/horizonclient"
)

type SubmitClass uint8

const (
	SubmitDefinite SubmitClass = iota
	SubmitAmbiguous
	SubmitTransport
)

func IsNotFound(err error) bool {
	status, ok := HTTPStatus(err)
	return ok && status == http.StatusNotFound
}

func HTTPStatus(err error) (int, bool) {
	var horizonErr *horizonclient.Error
	if !errors.As(err, &horizonErr) || horizonErr == nil {
		return 0, false
	}
	if horizonErr.Problem.Status != 0 {
		return horizonErr.Problem.Status, true
	}
	if horizonErr.Response == nil {
		return 0, false
	}
	if horizonErr.Response.StatusCode != 0 {
		return horizonErr.Response.StatusCode, true
	}
	statusFields := strings.Fields(horizonErr.Response.Status)
	if len(statusFields) > 0 {
		if status, err := strconv.Atoi(statusFields[0]); err == nil {
			return status, true
		}
	}
	return 0, false
}

func RetryAfter(err error) (time.Duration, bool) {
	var horizonErr *horizonclient.Error
	if !errors.As(err, &horizonErr) || horizonErr == nil || horizonErr.Response == nil {
		return 0, false
	}
	value := strings.TrimSpace(horizonErr.Response.Header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	if delay, err := time.ParseDuration(value + "s"); err == nil {
		if delay < 0 {
			return 0, false
		}
		return delay, true
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := time.Until(retryAt)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func IsRetryableTransport(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	//nolint:staticcheck // netErr.Temporary deprecated but still signals retriable network issues
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func ClassifySubmitError(err error) SubmitClass {
	if status, ok := HTTPStatus(err); ok {
		if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
			return SubmitAmbiguous
		}
		return SubmitDefinite
	}
	if IsRetryableTransport(err) {
		return SubmitTransport
	}
	return SubmitDefinite
}

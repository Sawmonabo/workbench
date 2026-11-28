// Package download fetches bounded HTTPS resources and reports a server's
// answers neutrally; the caller that knows the server names what they mean.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// MaxSize is the most one download may bring.
const MaxSize int64 = 128 << 20

// ErrNotModified is [Get]'s answer when the server says the resource has not
// changed since the copy a conditional request names.
var ErrNotModified = operation.Fail(operation.ExitFailed, "download", "Not modified")

// StatusError is [Get]'s answer for a status other than 200 OK and 304 Not
// Modified: a "download" failure that names the status, with the response's
// header, so a caller that knows the server can say what the status means.
type StatusError struct {
	Status int
	Header http.Header
	err    error
}

func (e *StatusError) Error() string { return e.err.Error() }

// Unwrap is the [operation.Error] that reports the status.
func (e *StatusError) Unwrap() error { return e.err }

// Request is what [Get] sends beside the URL: the Accept header when Accept
// is set, Token as a bearer credential when set, and every field of Header;
// it reports the bytes it receives to Progress when set.
type Request struct {
	Accept, Token string
	Header        http.Header
	Progress      func(done, total int64)
}

// Get fetches the HTTPS resource at location, at most limit bytes and never
// more than [MaxSize], as r says, and returns its body and the response's
// header. A 304 Not Modified, the answer to a request header makes
// conditional, is [ErrNotModified], with the response's header; any other
// status but 200 OK is a [StatusError]. Redirects to another host drop the
// credential. The caller bounds the time it may take through ctx; a download
// that receives nothing for a minute stops.
func Get(
	ctx context.Context,
	location string,
	limit int64,
	r Request,
) ([]byte, http.Header, error) {
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || limit < 1 ||
		limit > MaxSize {
		return nil, nil, operation.Fail(
			operation.ExitInvalid,
			"download",
			"Downloads require HTTPS, no URL credentials, and a bounded size",
		)
	}
	outgoing, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, nil, err
	}
	for name, values := range r.Header {
		outgoing.Header[http.CanonicalHeaderKey(name)] = values
	}
	if r.Accept != "" {
		outgoing.Header.Set("Accept", r.Accept)
	}
	if r.Token != "" {
		outgoing.Header.Set("Authorization", "Bearer "+r.Token)
	}
	// A stall limit, not a total-time limit: a slow link may take long as long
	// as bytes keep arriving.
	download, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	outgoing = outgoing.WithContext(download)
	stall := time.AfterFunc(stallLimit, func() { cancel(errStalled) })
	defer stall.Stop()
	response, err := downloadClient.Do(outgoing)
	if err != nil {
		if err := cut(ctx, download); err != nil {
			return nil, nil, err
		}
		return nil, nil, operation.Fail(
			operation.ExitFailed,
			"download",
			"HTTPS download failed: "+err.Error(),
		)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotModified {
		return nil, response.Header, ErrNotModified
	}
	if response.StatusCode != http.StatusOK {
		return nil, nil, &StatusError{
			Status: response.StatusCode,
			Header: response.Header,
			err: operation.Fail(
				operation.ExitFailed,
				"download",
				fmt.Sprintf("Download unavailable (HTTP %d)", response.StatusCode),
			),
		}
	}
	if response.ContentLength > limit {
		return nil, nil, operation.Fail(
			operation.ExitFailed,
			"download",
			"Download exceeds its size bound",
		)
	}
	body := counted(
		&stallReader{body: response.Body, timer: stall},
		response.ContentLength,
		r.Progress,
	)
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err := cut(ctx, download); err != nil {
		return nil, nil, err
	}
	if err != nil || int64(len(data)) > limit {
		return nil, nil, operation.Fail(
			operation.ExitFailed,
			"download",
			"Download exceeds its size bound or is incomplete",
		)
	}
	return data, response.Header, nil
}

// stallLimit is how long a download may go without receiving a byte.
const stallLimit = time.Minute

// downloadClient follows at most five redirects, each to HTTPS.
var downloadClient = &http.Client{
	Transport: downloadTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return operation.Fail(operation.ExitInvalid, "download", "Unsafe download redirect")
		}
		return nil
	},
}

// downloadTransport is the default transport's proxy, dial, TLS and idle
// behavior plus a response-header deadline, shared so connections are reused.
var downloadTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          10,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: time.Second,
	ResponseHeaderTimeout: stallLimit,
}

var errStalled = errors.New("download stalled")

func stalledDownload() error {
	return operation.Fail(
		operation.ExitFailed,
		"download",
		"Download stalled for a minute; check the network and rerun",
	)
}

// stallReader restarts the stall timer after every successful read.
type stallReader struct {
	body  io.Reader
	timer *time.Timer
}

func (r *stallReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.timer.Reset(stallLimit)
	}
	return n, err
}

// cut is why a download stopped early, nil when it did not: the caller's
// context ended (an interruption, which keeps its exit), or download was
// cancelled after stallLimit without data.
func cut(caller, download context.Context) error {
	if errors.Is(context.Cause(download), errStalled) {
		return stalledDownload()
	}
	return caller.Err()
}

// counted is body reporting its bytes to progress, or body when progress is
// nil.
func counted(body io.Reader, total int64, progress func(done, total int64)) io.Reader {
	if progress == nil {
		return body
	}
	return &countingReader{body: body, total: total, progress: progress}
}

// countingReader reports the bytes read so far and the expected total, -1 when
// unknown; at the end of an unsized body it reports the count as the total.
type countingReader struct {
	body     io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.done += int64(n)
	total := r.total
	end := errors.Is(err, io.EOF) && total < 0
	if end {
		total = r.done
	}
	if n > 0 || end {
		r.progress(r.done, total)
	}
	return n, err
}

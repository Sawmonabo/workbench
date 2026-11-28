// Package download fetches bounded HTTPS resources and reports a server's
// answers neutrally; the caller that knows the server names what they mean.
// Every failure reads the same way: the address, then the cause in plain
// words, such as "https://example.com/page: HTTP 404 Not Found" or
// "https://example.com/page: connection refused".
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// MaxSize is the most one download may bring.
const MaxSize int64 = 128 << 20

// ErrNotModified matches [Get]'s answer when the server says the resource has
// not changed since the copy a conditional request names.
var ErrNotModified = errors.New("HTTP 304 Not Modified")

// StatusError is [Get]'s answer for a status other than 200 OK: a "download"
// failure that names the address and the status, with the response's header,
// so a caller that knows the server can say what the status means. A 304 Not
// Modified matches [ErrNotModified].
type StatusError struct {
	Status int
	Header http.Header
	err    error
}

func (e *StatusError) Error() string { return e.err.Error() }

// Unwrap is the [operation.Error] that reports the status.
func (e *StatusError) Unwrap() error { return e.err }

// Is reports whether target is [ErrNotModified] and the status 304.
func (e *StatusError) Is(target error) bool {
	return target == ErrNotModified && e.Status == http.StatusNotModified
}

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
// header. A status other than 200 OK is a [StatusError], with the response's
// header; a 304 Not Modified, the answer to a request header makes
// conditional, matches [ErrNotModified]. Redirects to another host drop the
// credential. The caller bounds the time it may take through ctx, and a
// download past that deadline has timed out; one that receives nothing for a
// minute stops. Only an interruption, ctx's cancellation, is returned as
// ctx's error, so it keeps its exit.
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
			shown(u, err)+
				": refused; a download needs an HTTPS address with no credentials or fragment, and a bounded size",
		)
	}
	outgoing, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, nil, failure(location, err.Error())
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
		if err := cut(location, ctx, download); err != nil {
			return nil, nil, err
		}
		return nil, nil, failure(location, cause(err))
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, response.Header, &StatusError{
			Status: response.StatusCode,
			Header: response.Header,
			err:    failure(location, statusLine(response.StatusCode)),
		}
	}
	tooLarge := failure(location, "larger than its "+display.Size(limit)+" bound")
	if response.ContentLength > limit {
		return nil, nil, tooLarge
	}
	body := counted(
		&stallReader{body: response.Body, timer: stall},
		response.ContentLength,
		r.Progress,
	)
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err := cut(location, ctx, download); err != nil {
		return nil, nil, err
	}
	switch {
	case err != nil:
		return nil, nil, failure(location, "ended early ("+cause(err)+")")
	case int64(len(data)) > limit:
		return nil, nil, tooLarge
	}
	return data, response.Header, nil
}

// failure is [Get]'s answer for a download from location that did not
// succeed: a "download" failure that names the address, then the cause.
func failure(location, cause string) error {
	return operation.Fail(operation.ExitFailed, "download", location+": "+cause)
}

// shown is the address a refusal of u names: u without the credentials it
// may carry, or "The download address" when it is not a URL.
func shown(u *url.URL, err error) string {
	if err != nil {
		return "The download address"
	}
	address := *u
	address.User = nil
	return address.String()
}

// statusLine is the status code as an HTTP status line names it, such as
// "HTTP 404 Not Found".
func statusLine(code int) string {
	if text := http.StatusText(code); text != "" {
		return fmt.Sprintf("HTTP %d %s", code, text)
	}
	return fmt.Sprintf("HTTP %d", code)
}

// cause is why a request failed, in plain words: "timed out" for a timeout,
// "host NAME not found", the operating system's reason, such as "connection
// refused", or the error itself. A [url.Error]'s own address, which may be a
// redirect's, is left out: [Get] names the address its caller asked for.
func cause(err error) string {
	if problem, ok := errors.AsType[*url.Error](err); ok {
		err = problem.Err
	}
	var timeout interface{ Timeout() bool }
	var host *net.DNSError
	var errno syscall.Errno
	switch {
	case errors.As(err, &timeout) && timeout.Timeout():
		return "timed out"
	case errors.As(err, &host) && host.IsNotFound:
		return "host " + host.Name + " not found"
	case errors.As(err, &errno):
		return errno.Error()
	}
	return err.Error()
}

// stallLimit is how long a download may go without receiving a byte.
const stallLimit = time.Minute

// downloadClient follows at most five redirects, each to HTTPS.
var downloadClient = &http.Client{
	Transport: downloadTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		switch {
		case len(via) >= 5:
			return errors.New("redirected more than five times")
		case req.URL.Scheme != "https":
			return errors.New("redirected away from HTTPS")
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

// cut is why the download from location stopped early, nil when it did not:
// it received nothing for stallLimit, the caller's deadline passed, or the
// caller's context was cancelled (an interruption, which keeps its exit).
func cut(location string, caller, download context.Context) error {
	switch {
	case errors.Is(context.Cause(download), errStalled):
		return failure(location, "received nothing for a minute; check the network and rerun")
	case errors.Is(caller.Err(), context.DeadlineExceeded):
		return failure(location, "timed out")
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

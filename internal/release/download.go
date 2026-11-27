package release

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

// Download fetches a bounded public HTTPS resource; callers verify its content.
// progress, when set, is told the bytes received and the size the server sent
// (-1 when it sent none).
func Download(
	ctx context.Context,
	location string,
	limit int64,
	progress func(done, total int64),
) ([]byte, error) {
	data, _, err := get(
		ctx,
		location,
		limit,
		request{accept: "application/octet-stream", progress: progress},
	)
	return data, err
}

// Get fetches the HTTPS resource at location, at most limit bytes, as
// [Download] does, sending accept as its Accept header when set and every
// field of header, and returns its body and the response's header. A 304 Not
// Modified, the answer to a request header makes conditional, is
// [ErrNotModified], with the response's header. The caller bounds the time it
// may take through ctx.
func Get(
	ctx context.Context,
	location string,
	limit int64,
	accept string,
	header http.Header,
) ([]byte, http.Header, error) {
	return get(ctx, location, limit, request{accept: accept, header: header})
}

// errNotFound is get's answer for a resource the server says does not exist.
var errNotFound = operation.Fail(operation.ExitBlocked, "release_unavailable", "Not found")

// ErrNotModified is [Get]'s answer when the server says the resource has not
// changed since the copy a conditional request names.
var ErrNotModified = operation.Fail(operation.ExitFailed, "download", "Not modified")

// request is what get sends beside the URL: the Accept header when accept is
// set, token as a bearer credential when set, and every field of header; it
// reports the bytes it receives to progress when set.
type request struct {
	accept, token string
	header        http.Header
	progress      func(done, total int64)
}

// get fetches a bounded HTTPS resource as r says and returns its body and the
// response's header. Redirects to another host drop the credential.
func get(
	ctx context.Context,
	location string,
	limit int64,
	r request,
) ([]byte, http.Header, error) {
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || limit < 1 ||
		limit > MaxDownload {
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
	for name, values := range r.header {
		outgoing.Header[http.CanonicalHeaderKey(name)] = values
	}
	if r.accept != "" {
		outgoing.Header.Set("Accept", r.accept)
	}
	if r.token != "" {
		outgoing.Header.Set("Authorization", "Bearer "+r.token)
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
	if response.StatusCode == http.StatusNotFound {
		return nil, nil, errNotFound
	}
	if response.StatusCode == http.StatusForbidden &&
		response.Header.Get("X-RateLimit-Remaining") == "0" {
		return nil, nil, operation.Fail(
			operation.ExitFailed,
			"download",
			"GitHub's request limit is used up for now; wait an hour, or log in with gh auth login",
		)
	}
	if r.token != "" && (response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden) {
		return nil, nil, operation.Fail(
			operation.ExitFailed,
			"download",
			fmt.Sprintf(
				"GitHub refused the request (HTTP %d); check gh auth status or the GH_TOKEN/GITHUB_TOKEN variables",
				response.StatusCode,
			),
		)
	}
	if response.StatusCode != http.StatusOK {
		return nil, nil, operation.Fail(
			operation.ExitFailed,
			"download",
			fmt.Sprintf("Download unavailable (HTTP %d)", response.StatusCode),
		)
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
		r.progress,
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

// Package download fetches bounded HTTPS resources and reports a server's
// answers neutrally; the caller that knows the server names what they mean.
// Every failure reads the same way: the address, then the cause in plain
// words, such as "https://example.com/page: HTTP 404 Not Found" or
// "https://example.com/page: connection refused".
package download

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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
			"Workbench refuses to download "+shown(u, err),
			operation.Cause("a download needs an HTTPS address with no credentials or "+
				"fragment, and a bounded size"),
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
	// as bytes keep arriving. The one timer covers the wait for the response's
	// header as well as each read of its body, restarting when the header
	// arrives and after every read that brings bytes, so a server that sends
	// nothing for a minute, before its header or after, always stops the
	// download the same way.
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
		text, fix := cause(err)
		return nil, nil, failure(location, text, fix...)
	}
	stall.Reset(stallLimit)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, response.Header, &StatusError{
			Status: response.StatusCode,
			Header: response.Header,
			err:    failure(location, statusLine(response.StatusCode)),
		}
	}
	tooLarge := failure(location, "larger than its "+display.Limit(limit)+" bound")
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
		text, fix := cause(err)
		return nil, nil, failure(location, "ended early: "+text, fix...)
	case int64(len(data)) > limit:
		return nil, nil, tooLarge
	}
	return data, response.Header, nil
}

// failure is [Get]'s answer for a download from location that did not
// succeed: a "download" failure that names the address, with cause and parts
// its body.
func failure(location, cause string, parts ...operation.Part) error {
	return operation.Fail(operation.ExitFailed, "download", "Cannot download "+location,
		append([]operation.Part{operation.Cause(cause)}, parts...)...)
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

// cause is why a request failed, in words a person can act on, naming the
// host that failed, and the fix lines that help: a name that does not resolve
// or a name server that cannot be asked, a host or proxy that refuses or
// closes the connection, cannot be reached or does not answer in time, a
// certificate this system does not accept, or, failing those, the error
// itself. A
// [url.Error]'s own address, which may be a redirect's, is left out: [Get]
// names the address its caller asked for; its host is the one named.
func cause(err error) (string, []operation.Part) {
	host := "the server"
	if problem, ok := errors.AsType[*url.Error](err); ok {
		if u, parseErr := url.Parse(problem.URL); parseErr == nil && u.Hostname() != "" {
			host = u.Hostname()
		}
		err = problem.Err
	}
	network := []operation.Part{operation.FixText("check the network")}
	// A proxy's failure is the transport's "proxyconnect" error around the
	// dial to the proxy, whose address it names.
	if outer, ok := errors.AsType[*net.OpError](err); ok && outer.Op == "proxyconnect" {
		host = "the proxy"
		if inner, ok := errors.AsType[*net.OpError](outer.Err); ok && inner.Addr != nil {
			host += " at " + inner.Addr.String()
		}
		network = []operation.Part{operation.FixText("check the HTTPS_PROXY variable")}
	}
	var timeout interface{ Timeout() bool }
	var errno syscall.Errno
	switch dns, isDNS := errors.AsType[*net.DNSError](err); {
	case isDNS && dns.IsNotFound:
		return "host " + dns.Name + " not found", network
	case isDNS && dns.IsTimeout:
		return "the name server did not answer for " + dns.Name, network
	case isDNS:
		// Err is the lookup's own reason as text, its last clause the
		// system's, such as "network is unreachable" or "server misbehaving".
		reason := dns.Err[strings.LastIndex(dns.Err, ": ")+1:]
		return "cannot look up " + dns.Name + ": " + strings.TrimSpace(reason), network
	}
	if text, fix, ok := certificateCause(host, err); ok {
		return text, fix
	}
	switch {
	case errors.As(err, &timeout) && timeout.Timeout():
		return host + " did not answer in time", network
	case errors.As(err, &errno) && errno == syscall.ECONNREFUSED:
		return host + " refused the connection", network
	case errors.As(err, &errno) && errno == syscall.ECONNRESET,
		errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return host + " closed the connection", network
	case errors.As(err, &errno):
		return "cannot reach " + host + ": " + errno.Error(), network
	}
	return err.Error(), nil
}

// certificateCause is the cause, and its fix lines, of a secure connection to
// host that failed for its certificate or its TLS, and ok when err is one: a
// certificate this system's authorities do not sign, which a proxy or
// security software that reads HTTPS sends; one for other names; one past
// or before its dates, which a wrong clock makes; or an answer that is not
// TLS.
func certificateCause(host string, err error) (string, []operation.Part, bool) {
	intercept := []operation.Part{
		operation.FixText("check for a proxy or security software that intercepts HTTPS"),
	}
	var authority x509.UnknownAuthorityError
	var name x509.HostnameError
	var record tls.RecordHeaderError
	switch invalid, isInvalid := errors.AsType[x509.CertificateInvalidError](err); {
	case errors.As(err, &authority):
		return "the certificate sent for " + host + " is not signed by an authority this " +
			"system trusts", intercept, true
	case errors.As(err, &name) && name.Certificate != nil:
		names := name.Certificate.DNSNames
		if len(names) == 0 {
			names = []string{name.Certificate.Subject.CommonName}
		}
		return "the certificate sent for " + host + " is for " + display.List(
			names,
		), intercept, true
	case isInvalid && invalid.Reason == x509.Expired:
		return "the certificate sent for " + host + " has expired or is not valid yet", []operation.Part{
			operation.FixText("check this computer's date and time"),
		}, true
	case isInvalid:
		return "the certificate sent for " + host + " is not valid: " + invalid.Error(), intercept, true
	case errors.As(err, &record):
		return host + " did not answer in TLS", intercept, true
	}
	return "", nil, false
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
// behavior, shared so connections are reused. It sets no response-header
// deadline: [Get]'s stall timer bounds that wait, so the one case of a server
// that sends nothing has one timer and one answer.
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
		return failure(location, "received nothing for a minute",
			operation.FixText("check the network"))
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

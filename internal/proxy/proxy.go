// Package proxy implements the passthrough proxy.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Long default timeout, to ensure client times out before the proxy.
const (
	DefaultUpstreamTimeout = 80 * time.Second
	DefaultMaxRequestBody  = 1 << 20  // 1 MiB
	DefaultMaxResponseBody = 16 << 20 // 16 MiB; list endpoints can be large
)

// Config of the proxy
type Config struct {
	Listen          string
	Upstream        string
	UpstreamTimeout time.Duration
	Logger          *slog.Logger
	MaxRequestBody  int64
	MaxResponseBody int64
}

// upstreamResponse is a fully read upstream response. The upstream body is already closed.
type upstreamResponse struct {
	status        int
	header        http.Header
	contentLength int64
	body          []byte
}

// Validate validates the proxy configuration
// TODO: Validate the max body sizes and add reasonable defaults
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address is required")
	}
	if _, err := parseUpstream(c.Upstream); err != nil {
		return err
	}
	return nil
}

// Proxy passes requests through to the upstream target
type Proxy struct {
	upstream        *url.URL
	transport       http.RoundTripper
	upstreamTimeout time.Duration
	log             *slog.Logger
	maxRequestBody  int64
	maxResponseBody int64
}

// New returns a new proxy with the given config
func New(cfg Config) (*Proxy, error) {
	err := cfg.Validate()
	if err != nil {
		return nil, err
	}

	upstream, err := parseUpstream(cfg.Upstream)
	if err != nil {
		return nil, err
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.UpstreamTimeout <= 0 {
		cfg.UpstreamTimeout = DefaultUpstreamTimeout
	}
	if cfg.MaxRequestBody <= 0 {
		cfg.MaxRequestBody = DefaultMaxRequestBody
	}
	if cfg.MaxResponseBody <= 0 {
		cfg.MaxResponseBody = DefaultMaxResponseBody
	}
	// TODO: Swap the req/resp body sizes to the cfg ones when they're actually configurable
	p := &Proxy{
		upstream:        upstream,
		transport:       newTransport(),
		upstreamTimeout: cfg.UpstreamTimeout,
		maxRequestBody:  DefaultMaxRequestBody,
		maxResponseBody: DefaultMaxResponseBody,
		log:             cfg.Logger,
	}
	return p, nil
}

// parseUpstream parses the upstream string and validates that it is a bare origin.
func parseUpstream(raw string) (*url.URL, error) {
	const example = "https://api.example.com"
	if raw == "" {
		return nil, fmt.Errorf("upstream is empty; example: %s", example)
	}
	// default to http if a scheme isn't present
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// err includes the full input, which may contain credentials. Inner Err does not.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, fmt.Errorf("unable to parse upstream: %w", urlErr.Err)
		}
		return nil, errors.New("unable to parse upstream")
	}
	// Credentials in the URL could end up in logs.
	if u.User != nil {
		return nil, fmt.Errorf("upstream %q: credentials in the URL are not allowed", u.Redacted())
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("upstream %q: scheme must be http or https", u.Redacted())
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("upstream %q: no host; give an origin like %s", u.Redacted(), example)
	}
	// The forwarder sends the client's path as-is, a base path would be dropped.
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("upstream must be a bare origin like %s, with no path, query, or fragment", example)
	}
	u.Path = ""
	return u, nil
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	log := p.log.With("method", r.Method, "path", r.URL.Path)

	if r.Method == http.MethodConnect {
		log.Warn("refused CONNECT request")
		p.writeError(w, http.StatusMethodNotAllowed, "rudeproxy_forward_proxy_refused",
			"rudeproxy is not a forward proxy; CONNECT is not supported")
		return
	}
	if r.URL.IsAbs() {
		log.Warn("refused absolute-form request", "target_host", r.URL.Host)
		p.writeError(w, http.StatusBadRequest, "rudeproxy_forward_proxy_refused",
			"rudeproxy is not a forward proxy; send requests to it directly as if it were the upstream")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, p.maxRequestBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			p.writeError(w, http.StatusRequestEntityTooLarge, "rudeproxy_request_too_large",
				fmt.Sprintf("rudeproxy: request body exceeds %d bytes", tooBig.Limit))
			return
		}
		log.Warn("reading request body failed", "err", err)
		p.writeError(w, http.StatusBadRequest, "rudeproxy_bad_request", "rudeproxy: could not read request body")
		return
	}

	resp, err := p.forward(r, body)
	if err != nil {
		log.Error("upstream request failed", "err", err, "duration", time.Since(start))
		p.writeError(w, http.StatusBadGateway, "rudeproxy_upstream_unavailable",
			"rudeproxy: upstream request failed: "+err.Error())
		return
	}

	writeResponse(w, r, resp)
	log.Info("forwarded", "status", resp.status, "duration", time.Since(start))
}

// writeResponse relays a buffered upstream response to the client.
func writeResponse(w http.ResponseWriter, in *http.Request, resp *upstreamResponse) {
	h := w.Header()
	for k, vv := range resp.header {
		h[k] = append([]string(nil), vv...)
	}
	removeHopByHop(h)

	h.Del("Content-Length")
	switch {
	case !bodyAllowedForStatus(resp.status):
	case in.Method == http.MethodHead:
		if resp.contentLength >= 0 {
			h.Set("Content-Length", strconv.FormatInt(resp.contentLength, 10))
		}
	default:
		h.Set("Content-Length", strconv.Itoa(len(resp.body)))
	}

	w.WriteHeader(resp.status)
	if len(resp.body) > 0 && in.Method != http.MethodHead {
		_, _ = w.Write(resp.body)
	}
}

func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status < 200:
		return false
	case status == http.StatusNoContent, status == http.StatusNotModified:
		return false
	}
	return true
}

func (p *Proxy) forward(in *http.Request, body []byte) (*upstreamResponse, error) {
	// Detach from the client's context. The upstream may still complete the operation, and we want to record that.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(in.Context()), p.upstreamTimeout)
	defer cancel()

	out, err := http.NewRequestWithContext(ctx, in.Method, p.upstream.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Copy the path and query in their original encoded form
	out.URL.Path = in.URL.Path
	out.URL.RawPath = in.URL.RawPath
	out.URL.RawQuery = in.URL.RawQuery

	out.Header = in.Header.Clone()
	removeHopByHop(out.Header)
	// Avoid the transport adding its own "Go-http-client" User-Agent
	if _, ok := out.Header["User-Agent"]; !ok {
		out.Header.Set("User-Agent", "")
	}

	// The transport retries some failed requests. A retry after the body was sent would be a second upstream submission the
	// event log would miss, so make the body non-rewindable. Retries where nothing was written yet stay possible.
	out.GetBody = nil

	resp, err := p.transport.RoundTrip(out)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, p.maxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading upstream response: %w", err)
	}
	if int64(len(respBody)) > p.maxResponseBody {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", p.maxResponseBody)
	}

	res := &upstreamResponse{
		status:        resp.StatusCode,
		header:        resp.Header,
		contentLength: resp.ContentLength,
		body:          respBody,
	}
	return res, nil
}

// TODO: when plugins are added, ensure they can format errors to match vedor error format.
// writeError sends a proxy-generated error.
func (p *Proxy) writeError(w http.ResponseWriter, status int, code, message string) {
	contentType, body := "application/json", neutralError(code, message)

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func newTransport() *http.Transport {
	t := &http.Transport{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 256,
		MaxConnsPerHost:     256,
		DisableCompression:  true,
		IdleConnTimeout:     90 * time.Second,
	}

	return t
}

// NewServer wraps h in an http.Server configured for the client-facing side.
func NewServer(h http.Handler, log *slog.Logger) *http.Server {
	// HTTP/1.1 only to support taking over the connection through http.Hijacker.
	var protocols http.Protocols
	protocols.SetHTTP1(true)

	return &http.Server{
		Handler:           h,
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// hopByHopHeaders are for a single connection not the message, so a proxy must not forward them (RFC 9110 section 7.6.1)
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// removeHopByHop strips hop-by-hop headers in place
func removeHopByHop(h http.Header) {
	for _, v := range h.Values("Connection") {
		for name := range strings.SplitSeq(v, ",") {
			if name = textproto.TrimString(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

// neutralError builds a neutral error envelope
func neutralError(code, message string) []byte {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	payload.Error.Code = code
	payload.Error.Message = message
	b, _ := json.Marshal(payload) // cannot fail: plain strings only
	return b
}

// Package proxy implements the passthrough proxy.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Long default timeout, to ensure client times out before the proxy.
const (
	DefaultUpstreamTimeout = 80 * time.Second
)

// Config of the proxy
type Config struct {
	Listen          string
	Upstream        string
	UpstreamTimeout time.Duration
	Logger          *slog.Logger
}

// Validate validates the proxy configuration
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

	p := &Proxy{
		upstream:        upstream,
		transport:       newTransport(),
		upstreamTimeout: cfg.UpstreamTimeout,
		log:             cfg.Logger,
	}
	return p, nil
}

// Run starts the proxy, until cancelled via context
func (p *Proxy) Run(ctx context.Context) error {
	// TODO: accept connections and forward them to the target.
	<-ctx.Done()
	return nil
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

func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// The transport would add "Accept-Encoding: gzip" and silently decompress the reply. Client would get different bytes and headers than the upstream sent.
	t.DisableCompression = true
	return t
}

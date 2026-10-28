// Package proxy implements the passthrough proxy.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
	if c.Upstream == "" {
		return errors.New("target address is required")
	}
	return nil
}

// Proxy passes requests through to the target
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

	upstream, err := ValidateUpstream(cfg.Upstream)
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

// ValidateUpstream parses the upstream string and validates that it is a bare origin.
func ValidateUpstream(raw string) (*url.URL, error) {
	const example = "https://api.example.com"
	if raw == "" {
		return nil, fmt.Errorf("upstream is empty; example: %s", example)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("upstream %q: scheme must be http or https", raw)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("upstream %q: no host; give an origin like %s", raw, example)
	}
	// Credentials in the URL could end up in logs.
	if u.User != nil {
		return nil, fmt.Errorf("upstream %q: credentials in the URL are not allowed", raw)
	}
	// The forwarder sends the client's path as-is, a base path would be dropped.
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("upstream %q: must be a bare origin like %s, with no path, query, or fragment", raw, example)
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

// Package proxy implements the passthrough proxy.
package proxy

import (
	"context"
	"errors"
	"fmt"
)

// Config of the proxy
type Config struct {
	Listen string
	Target string
}

// Validate validates the proxy configuration
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address is required")
	}
	if c.Target == "" {
		return errors.New("target address is required")
	}
	return nil
}

// Proxy passes requests through to the target
type Proxy struct {
	cfg Config
}

// New returns a new proxy with the given config
func New(cfg Config) *Proxy {
	return &Proxy{cfg: cfg}
}

// Run starts the proxy, until cancelled via context
func (p *Proxy) Run(ctx context.Context) error {
	if err := p.cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	// TODO: accept connections and forward them to the target.
	<-ctx.Done()
	return nil
}

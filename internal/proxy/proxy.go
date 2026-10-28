// Package proxy implements the passthrough proxy.
package proxy

import (
	"context"
	"errors"
	"fmt"
)

type Config struct {
	Listen string
	Target string
}

func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address is required")
	}
	if c.Target == "" {
		return errors.New("target address is required")
	}
	return nil
}

type Proxy struct {
	cfg Config
}

func New(cfg Config) *Proxy {
	return &Proxy{cfg: cfg}
}

func (p *Proxy) Run(ctx context.Context) error {
	if err := p.cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	// TODO: accept connections and forward them to the target.
	<-ctx.Done()
	return nil
}

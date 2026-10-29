package proxy

import (
	"errors"
	"net/http"
)

func (p *Proxy) shouldDrop() bool {
	// Don't waste time on the lock if there's 0 chance
	if p.responseDropRate == 0 {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rng.Float64() < p.responseDropRate
}

func dropConnection(w http.ResponseWriter) error {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("connection cannot be hijacked")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return err
	}
	return conn.Close()
}

package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{Listen: ":8080", Upstream: "localhost:9000"}, false},
		{"missing listen", Config{Upstream: "localhost:9000"}, true},
		{"missing upstream", Config{Listen: ":8080"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseUpstreamHidesCredentials(t *testing.T) {
	_, err := parseUpstream("https://user:secret@host:abc")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks credentials: %v", err)
	}
}

func TestParseUpstream(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "localhost:9000", want: "http://localhost:9000"},
		{raw: "https://api.example.com", want: "https://api.example.com"},
		{raw: "https://api.example.com/", want: "https://api.example.com"},
		{raw: "", wantErr: true},
		{raw: "ftp://api.example.com", wantErr: true},
		{raw: "https://user:pass@api.example.com", wantErr: true},
		{raw: "https://:443", wantErr: true},
		{raw: "https://api.example.com/v1", wantErr: true},
		{raw: "https://api.example.com?x=1", wantErr: true},
		{raw: "https://api.example.com#frag", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			u, err := parseUpstream(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseUpstream(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if err == nil && u.String() != tt.want {
				t.Errorf("parseUpstream(%q) = %q, want %q", tt.raw, u.String(), tt.want)
			}
		})
	}
}

// newTestProxy starts an upstream running h and returns a proxy in front of it.
func newTestProxy(t *testing.T, h http.HandlerFunc) *Proxy {
	t.Helper()
	upstream := httptest.NewServer(h)
	t.Cleanup(upstream.Close)
	p, err := New(Config{Listen: "127.0.0.1:0", Upstream: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// errorCode decodes the code from a proxy-generated error body.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("error body is not JSON: %v; body %q", err, rec.Body.String())
	}
	return payload.Error.Code
}

func TestServeHTTPForwards(t *testing.T) {
	p := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.URL.EscapedPath(); got != "/a%2Fb" {
			t.Errorf("path = %q, want /a%%2Fb", got)
		}
		if r.URL.RawQuery != "x=1&y=%20" {
			t.Errorf("query = %q, want x=1&y=%%20", r.URL.RawQuery)
		}
		if body, _ := io.ReadAll(r.Body); string(body) != "hello" {
			t.Errorf("body = %q, want hello", body)
		}
		if got := r.Header.Get("X-Keep"); got != "yes" {
			t.Errorf("X-Keep = %q, want yes", got)
		}
		for _, name := range []string{"X-Conn-Listed", "Keep-Alive", "Proxy-Authorization"} {
			if got := r.Header.Get(name); got != "" {
				t.Errorf("hop-by-hop header %s was forwarded: %q", name, got)
			}
		}
		if got := r.Header.Get("User-Agent"); got != "" {
			t.Errorf("User-Agent = %q, want none added", got)
		}
		w.Header().Set("X-Upstream", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "created")
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/a%2Fb?x=1&y=%20", strings.NewReader("hello"))
	req.Header.Set("X-Keep", "yes")
	req.Header.Set("Connection", "X-Conn-Listed")
	req.Header.Set("X-Conn-Listed", "drop me")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "created" {
		t.Errorf("body = %q, want created", rec.Body.String())
	}
	if got := rec.Header().Get("X-Upstream"); got != "1" {
		t.Errorf("X-Upstream = %q, want 1", got)
	}
}

func TestServeHTTPIgnoresClientCancel(t *testing.T) {
	p := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "done")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; the upstream call should not follow the client's cancellation", rec.Code)
	}
}

func TestServeHTTPRefusesForwardProxying(t *testing.T) {
	var hit atomic.Bool
	p := newTestProxy(t, func(http.ResponseWriter, *http.Request) { hit.Store(true) })

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{"CONNECT", http.MethodConnect, "/", http.StatusMethodNotAllowed},
		{"absolute form", http.MethodGet, "http://elsewhere.example.com/x", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if code := errorCode(t, rec); code != "rudeproxy_forward_proxy_refused" {
				t.Errorf("code = %q, want rudeproxy_forward_proxy_refused", code)
			}
		})
	}
	if hit.Load() {
		t.Error("refused request reached the upstream")
	}
}

func TestServeHTTPRequestTooLarge(t *testing.T) {
	var hit atomic.Bool
	p := newTestProxy(t, func(http.ResponseWriter, *http.Request) { hit.Store(true) })
	p.maxRequestBody = 4

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("hello")))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if code := errorCode(t, rec); code != "rudeproxy_request_too_large" {
		t.Errorf("code = %q, want rudeproxy_request_too_large", code)
	}
	if hit.Load() {
		t.Error("oversized request reached the upstream")
	}
}

func TestServeHTTPUpstreamFailures(t *testing.T) {
	t.Run("response too large", func(t *testing.T) {
		p := newTestProxy(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "0123456789!")
		})
		p.maxResponseBody = 10

		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		t.Cleanup(upstream.Close)
		p, err := New(Config{Listen: "127.0.0.1:0", Upstream: upstream.URL, UpstreamTimeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		upstream := httptest.NewServer(http.NotFoundHandler())
		upstream.Close()
		p, err := New(Config{Listen: "127.0.0.1:0", Upstream: upstream.URL})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
		if code := errorCode(t, rec); code != "rudeproxy_upstream_unavailable" {
			t.Errorf("code = %q, want rudeproxy_upstream_unavailable", code)
		}
	})
}

func TestWriteResponse(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		resp     *upstreamResponse
		wantCL   string
		wantBody string
	}{
		{
			name:     "GET uses buffered body length",
			method:   http.MethodGet,
			resp:     &upstreamResponse{status: http.StatusOK, contentLength: -1, body: []byte("hello")},
			wantCL:   "5",
			wantBody: "hello",
		},
		{
			name:   "HEAD keeps upstream length and sends no body",
			method: http.MethodHead,
			resp:   &upstreamResponse{status: http.StatusOK, contentLength: 42},
			wantCL: "42",
		},
		{
			name:   "204 has no length",
			method: http.MethodDelete,
			resp:   &upstreamResponse{status: http.StatusNoContent},
			wantCL: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.resp.header = http.Header{
				"Connection":        {"X-Conn-Listed"},
				"X-Conn-Listed":     {"drop me"},
				"Transfer-Encoding": {"chunked"},
				"Content-Length":    {"999"},
				"X-Keep":            {"yes"},
			}
			rec := httptest.NewRecorder()
			writeResponse(rec, httptest.NewRequestWithContext(t.Context(), tt.method, "/", nil), tt.resp)

			if rec.Code != tt.resp.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.resp.status)
			}
			if got := rec.Header().Get("Content-Length"); got != tt.wantCL {
				t.Errorf("Content-Length = %q, want %q", got, tt.wantCL)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
			if got := rec.Header().Get("X-Keep"); got != "yes" {
				t.Errorf("X-Keep = %q, want yes", got)
			}
			for _, name := range []string{"Connection", "X-Conn-Listed", "Transfer-Encoding"} {
				if got := rec.Header().Get(name); got != "" {
					t.Errorf("hop-by-hop header %s was relayed: %q", name, got)
				}
			}
		})
	}
}

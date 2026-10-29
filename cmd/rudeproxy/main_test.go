package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunCommands(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args", args: nil, wantCode: exitToolFailure, wantStderr: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: exitOK, wantStdout: version},
		{name: "help", args: []string{"help"}, wantCode: exitOK, wantStdout: "Usage:"},
		{name: "unknown command", args: []string{"bogus"}, wantCode: exitToolFailure, wantStderr: `unknown command "bogus"`},
		{name: "serve without upstream", args: []string{"serve"}, wantCode: exitToolFailure, wantStderr: "-upstream is required"},
		{name: "serve extra args", args: []string{"serve", "-upstream", "localhost:9000", "extra"}, wantCode: exitToolFailure, wantStderr: "unexpected arguments"},
		{name: "serve bad flag", args: []string{"serve", "-nope"}, wantCode: exitToolFailure, wantStderr: "-nope"},
		{name: "serve bad log level", args: []string{"serve", "-upstream", "localhost:9000", "-log-level", "loud"}, wantCode: exitToolFailure, wantStderr: "unexpected log-level"},
		{name: "serve invalid upstream", args: []string{"serve", "-upstream", "ftp://example.com"}, wantCode: exitToolFailure, wantStderr: "refusing to start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr %q", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunServeCredentialsNotLogged(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"serve", "-upstream", "https://user:secret@example.com"}, &stdout, &stderr)
	if code != exitToolFailure {
		t.Errorf("exit code = %d, want %d", code, exitToolFailure)
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Errorf("stderr leaks credentials: %q", stderr.String())
	}
}

// syncBuffer is a bytes.Buffer safe for the server goroutine to write while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunServeShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve", "-listen", "127.0.0.1:0", "-upstream", "localhost:9000"}, &stdout, &stderr)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stderr.String(), "rudeproxy listening") {
		if time.Now().After(deadline) {
			t.Fatalf("server did not start; stderr %q", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d, want %d; stderr %q", code, exitOK, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down after cancel")
	}
}

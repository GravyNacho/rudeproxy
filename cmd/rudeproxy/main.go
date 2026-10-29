// Command rudeproxy runs an intentionally flaky passthrough proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rudeproxy/internal/proxy"
)

// version is set at build. -ldflags "-X main.version=...".
var version = "dev"

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

const (
	exitOK          = 0
	exitToolFailure = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main, but with context/args/stdio etc passed in for tests
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitToolFailure
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "serve":
		return runServe(ctx, rest, stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return exitOK
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "rudeproxy: unknown command %q\n\n", cmd)
		usage(stderr)
		return exitToolFailure
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: rudeproxy <command> [flags]

Commands:
  serve     run the proxy in front of an upstream API
  version   print version information

Run "rudeproxy <command> -h" for a command's flags.
`)
}

func runServe(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// TODO: Add flags for configuring req/resp body size, and connection counts on the transport
	var (
		listen          = fs.String("listen", "127.0.0.1:8080", "address to listen on")
		upstream        = fs.String("upstream", "", "upstream address to forward to (required)")
		upstreamTimeout = fs.Duration("upstream-timeout", proxy.DefaultUpstreamTimeout, "how long to wait for the upstream, independent of the client")
		logLevel        = fs.String("log-level", "info", "minimum level to log: debug, info, warn, or error")
		dropRate        = fs.Float64("response-drop-rate", 0, "chance (0-1) of dropping the connection after the upstream responds")
		seed            = fs.Uint64("seed", 0, "random seed for faults; 0 picks one")
	)
	if err := fs.Parse(args); err != nil {
		return exitToolFailure
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rudeproxy serve: unexpected arguments: %v\n", fs.Args())
		return exitToolFailure
	}
	if *upstream == "" {
		fmt.Fprintln(stderr, "rudeproxy serve: -upstream is required (e.g. -upstream https://api.example.com)")
		return exitToolFailure
	}

	logOpts := &slog.HandlerOptions{}

	level, ok := logLevels[strings.ToLower(*logLevel)]
	if !ok {
		fmt.Fprintf(stderr, "rudeproxy serve: unexpected log-level value: %q. want debug, info, warn, or error\n", *logLevel)
		return exitToolFailure
	}
	logOpts.Level = level

	log := slog.New(slog.NewTextHandler(stderr, logOpts))

	if *dropRate == 0 {
		log.Warn("response drop rate set to 0, rudeproxy will act as a friendly passthrough proxy")
	}

	p, err := proxy.New(proxy.Config{
		Listen:           *listen,
		Upstream:         *upstream,
		UpstreamTimeout:  *upstreamTimeout,
		Logger:           log,
		ResponseDropRate: *dropRate,
		RudenessSeed:     *seed,
	})
	if err != nil {
		log.Error("refusing to start", "err", err)
		return exitToolFailure
	}
	log.Info("starting rudeproxy", "version", version, "listen", *listen, "upstream", *upstream, "log-level", logOpts.Level.Level().String())
	lnCfg := &net.ListenConfig{}
	ln, err := lnCfg.Listen(ctx, "tcp", *listen)
	if err != nil {
		log.Error("listen failed", "err", err)
		return exitToolFailure
	}

	srv := proxy.NewServer(p, log)
	log.Info("rudeproxy listening", "addr", ln.Addr().String(), "upstream", *upstream, "version", version)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		log.Error("server failed", "err", err)
		return exitToolFailure
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown did not complete cleanly", "err", err)
		return exitToolFailure
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		return exitToolFailure
	}
	return exitOK
}

// Command rudeproxy runs an intentionally flaky passthrough proxy.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"rudeproxy/internal/proxy"
	"strings"
	"syscall"
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
	flag := flag.NewFlagSet("serve", flag.ContinueOnError)
	flag.SetOutput(stderr)
	var (
		listen          = flag.String("listen", "127.0.0.1:8080", "address to listen on")
		upstream        = flag.String("upstream", "", "upstream address to forward to (required)")
		upstreamTimeout = flag.Duration("upstream-timeout", proxy.DefaultUpstreamTimeout, "how long to wait for the upstream, independent of the client")
		logLevel        = flag.String("log-level", "info", "minimum level to log: debug, info, warn, or error")
	)
	if err := flag.Parse(args); err != nil {
		return exitToolFailure
	}
	if flag.NArg() > 0 {
		fmt.Fprintf(stderr, "rudeproxy serve: unexpected arguments: %v\n", flag.Args())
		return exitToolFailure
	}
	if *upstream == "" {
		fmt.Fprintln(stderr, "rudeproxy serve: -upstream is required (e.g. -target https://api.example.com)")
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
	log.Log(ctx, logOpts.Level.Level(), fmt.Sprintf("logging initialized at log-level %s", logOpts.Level.Level().String()))

	log.Info("starting rudeproxy", "version", version, "listen", *listen, "upstream", *upstream)
	// TODO: thowing away the ctx for now, as we aren't using it yet. Circle back when starting the actual proxy-ing work.
	_, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// TODO: thowing away the proxy for now, as we aren't using it yet. Circle back when starting the actual proxy-ing work.
	_, err := proxy.New(proxy.Config{
		Listen:          *listen,
		Upstream:        *upstream,
		UpstreamTimeout: *upstreamTimeout,
		Logger:          log,
	})
	if err != nil {
		log.Error("refusing to start", "err", err)
		return exitToolFailure
	}

	return exitOK
}

// Command rudeproxy runs an intentionally flaky passthrough proxy.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"rudeproxy/internal/proxy"
	"syscall"
)

// version is set at build. -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		listen      = flag.String("listen", "127.0.0.1:8080", "address to listen on")
		target      = flag.String("target", "", "upstream address to forward to (required)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if *target == "" {
		flag.Usage()
		return fmt.Errorf("-target is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	p := proxy.New(proxy.Config{
		Listen: *listen,
		Target: *target,
	})

	slog.Info("starting rudeproxy", "version", version, "listen", *listen, "target", *target)
	return p.Run(ctx)
}

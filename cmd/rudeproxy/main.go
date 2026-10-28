// Command rudeproxy runs an intentionally flaky passthrough proxy.
package main

import (
	"errors"
	"log/slog"
	"os"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	return errors.New("Not implemented")
}

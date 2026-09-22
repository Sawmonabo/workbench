// Workbench coordinates chezmoi and native package managers for developer
// machines and existing projects.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

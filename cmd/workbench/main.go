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
	// Workbench refuses group- or world-writable parents, tools and targets,
	// so what it and its tools create must never be: keep the user's umask and
	// add 022 to it, as sudo does. Ubuntu logins default to 002.
	syscall.Umask(syscall.Umask(0) | 0o022)
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGHUP,
	)
	defer stop()
	return cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

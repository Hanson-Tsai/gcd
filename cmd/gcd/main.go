// Command gcd syncs a local folder with a private GitHub repo.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Hanson-Tsai/gcd/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

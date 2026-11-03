// Command gcd syncs a local folder with a private GitHub repo.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

func main() {
	// git runs gcd as its GIT_ASKPASS to fetch the token (see gitstore).
	if gitstore.ServeAskpass() {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

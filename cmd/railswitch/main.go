// Command railswitch manages Rails dev environments on rootless podman.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jbigler/railswitch/internal/cli"
)

// stopSignals cancel the command's context. SIGHUP is among them: a closed
// terminal or dashboard panel hangs up, and Go's default exit would leave
// the commands railswitch started (each in its own group) running.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	code := cli.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// Command pbmanager is both the management daemon and the root recovery CLI.
package main

import (
	"os"
	"syscall"

	"github.com/apexo/pbmanager/internal/cli"
)

var version = "dev"

func main() {
	// All files created by the manager and its children should be private by
	// default. Individual artifacts may be relaxed deliberately.
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	cli.Version = version
	os.Exit(cli.Run(os.Args[1:]))
}

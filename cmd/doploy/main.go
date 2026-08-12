// Command doploy provisions DigitalOcean infrastructure from a compose-style
// spec file and deploys containers onto it over SSH.
package main

import (
	"fmt"
	"os"

	"github.com/jeremyaboyd/doploy/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

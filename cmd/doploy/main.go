// Command doploy provisions DigitalOcean infrastructure from a compose-style
// spec file and deploys containers onto it over SSH.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/jeremyaboyd/doploy/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// resolveVersion prefers an ldflags-stamped version, then the module version
// Go embeds for `go install module@version` builds, so installs from a tag
// report that tag without any release tooling.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	if err := cli.Execute(resolveVersion()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

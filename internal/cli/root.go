// Package cli defines doploy's command tree.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/spf13/cobra"
)

// appVersion is set once by Execute and used for the API user agent.
var appVersion = "dev"

// outputFormat is the value of the global --output flag.
var outputFormat string

const (
	formatTable = "table"
	formatJSON  = "json"
)

// Execute builds and runs the command tree.
func Execute(version string) error {
	appVersion = version

	root := &cobra.Command{
		Use:   "doploy",
		Short: "Provision DigitalOcean infrastructure and deploy containers to it",
		Long: `doploy takes a compose-style spec file, provisions the droplets it describes,
and deploys your containers onto them over SSH.

The binary is named doploy rather than do because "do" is a reserved word in
bash and zsh. Add an alias in a shell where it does not collide.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", formatTable,
		"output format: table or json")

	root.AddCommand(
		newAuthCmd(),
		newAddCmd(),
		newListCmd(),
		newDeployCmd(),
		newDestroyCmd(),
		newCalculateCmd(),
	)

	return root.ExecuteContext(signalContext())
}

// signalContext cancels on Ctrl-C so a long provision or deploy can be
// interrupted without leaving the terminal wedged.
func signalContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		fmt.Fprintln(os.Stderr, "\ninterrupted; stopping after the current step")
		cancel()
	}()

	return ctx
}

// jsonOutput reports whether --output json was requested.
func jsonOutput() bool { return outputFormat == formatJSON }

// validateOutput rejects an unknown --output value early.
func validateOutput() error {
	switch outputFormat {
	case formatTable, formatJSON:
		return nil
	default:
		return fmt.Errorf("unknown --output %q: use table or json", outputFormat)
	}
}

// apiClient builds an authenticated client, turning the "no credentials" case
// into a message that says what to do about it.
func apiClient() (*godo.Client, error) {
	client, err := doclient.New(appVersion)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// confirm asks a yes/no question. It returns false without prompting when
// stdin is not interactive, so scripted runs must pass --yes explicitly rather
// than hanging or silently proceeding.
func confirm(prompt string) (bool, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false, err
	}
	if stat.Mode()&os.ModeCharDevice == 0 {
		return false, fmt.Errorf("stdin is not a terminal; pass --yes to proceed without confirmation")
	}

	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, scanner.Err()
	}

	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

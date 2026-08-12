package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jeremyaboyd/doploy/internal/config"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage DigitalOcean credentials",
	}
	cmd.AddCommand(newAuthInitCmd(), newAuthStatusCmd(), newAuthLogoutCmd())
	return cmd
}

func newAuthInitCmd() *cobra.Command {
	var token string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Authenticate with DigitalOcean",
		Long: `Stores a DigitalOcean API token for doploy to use.

Create a token with read and write scope at:
  https://cloud.digitalocean.com/account/api/tokens

The token is validated against the API before it is saved, and written to
` + config.Path() + ` with owner-only permissions.

OAuth is not wired up yet: it needs a registered DigitalOcean OAuth
application. The credential store already carries the fields for it, so
switching over later will not invalidate anything saved now.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				var err error
				token, err = promptToken()
				if err != nil {
					return err
				}
			}
			token = strings.TrimSpace(token)
			if token == "" {
				return errors.New("no token provided")
			}

			client, err := doclient.NewWithToken(token, appVersion)
			if err != nil {
				return err
			}

			ui.Step("Validating token")
			account, err := doclient.Account(cmd.Context(), client)
			if err != nil {
				return fmt.Errorf("the token was rejected by the API: %w", err)
			}

			creds := &config.Credentials{
				Method:       config.MethodPAT,
				Token:        token,
				AccountEmail: account.Email,
				AccountUUID:  account.UUID,
			}
			if err := config.Save(creds); err != nil {
				return err
			}

			fmt.Printf("Authenticated as %s\nCredentials saved to %s\n", account.Email, config.Path())
			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "",
		"API token; prompts interactively when omitted")
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the authenticated account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}

			creds, err := config.Load()
			if err != nil {
				return err
			}

			client, err := doclient.NewWithToken(creds.Token, appVersion)
			if err != nil {
				return err
			}
			account, err := doclient.Account(cmd.Context(), client)
			if err != nil {
				return fmt.Errorf("the stored token is no longer valid: %w", err)
			}

			source := config.Path()
			if creds.FromEnv {
				source = "environment (" + config.EnvToken + ")"
			}

			if jsonOutput() {
				return ui.JSON(map[string]any{
					"email":          account.Email,
					"uuid":           account.UUID,
					"status":         account.Status,
					"email_verified": account.EmailVerified,
					"droplet_limit":  account.DropletLimit,
					"method":         string(creds.Method),
					"source":         source,
				})
			}

			table := ui.NewTable("FIELD", "VALUE")
			table.Row("email", account.Email)
			table.Row("uuid", account.UUID)
			table.Row("status", account.Status)
			table.Row("email verified", account.EmailVerified)
			table.Row("droplet limit", account.DropletLimit)
			if account.Team != nil && account.Team.Name != "" {
				table.Row("team", account.Team.Name)
			}
			table.Row("auth method", creds.Method)
			table.Row("credentials", source)
			table.Print()
			return nil
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Delete(); err != nil {
				return err
			}
			fmt.Println("Removed", config.Path())

			// A token in the environment survives logout, which would otherwise
			// look like the logout silently failed.
			if os.Getenv(config.EnvToken) != "" || os.Getenv(config.EnvTokenAlt) != "" {
				ui.Warn("a token is still set in the environment; unset %s to fully sign out", config.EnvToken)
			}
			return nil
		},
	}
}

// promptToken reads a token from the terminal without echoing it.
func promptToken() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("stdin is not a terminal; pass --token or set %s", config.EnvToken)
	}

	fmt.Fprint(os.Stderr, "DigitalOcean API token: ")
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading token: %w", err)
	}
	return string(raw), nil
}

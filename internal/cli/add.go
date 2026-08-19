package cli

import (
	"context"
	"fmt"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/sshx"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create resources on the account",
	}
	cmd.AddCommand(newAddSSHCmd())
	return cmd
}

func newAddSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ssh [name]",
		Aliases: []string{"key"},
		Short:   "Generate an SSH key and upload it to DigitalOcean",
		Long: `Generates a new ed25519 keypair, stores the private key in doploy's key
store, and uploads the public key to the DigitalOcean account under the given
name (or a generated one).

Keys in the store are offered automatically when deploying, so after

    doploy add ssh mykey

a spec only needs

    defaults:
      ssh_keys: [mykey]

and deploys work with no --ssh-key flag. The private key never leaves this
machine; only the public half is uploaded.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}

			var name string
			if len(args) == 1 {
				name = args[0]
				if err := sshx.ValidateKeyName(name); err != nil {
					return err
				}
			} else {
				generated, err := sshx.RandomKeyName()
				if err != nil {
					return err
				}
				name = generated
			}

			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			// A name collision on the account would leave two keys
			// indistinguishable in a spec's ssh_keys list, so check first.
			if err := ensureAccountKeyNameFree(ctx, client, name); err != nil {
				return err
			}

			key, err := sshx.GenerateStoredKey(name)
			if err != nil {
				return err
			}

			created, _, err := client.Keys.Create(ctx, &godo.KeyCreateRequest{
				Name:      name,
				PublicKey: key.PublicKey,
			})
			if err != nil {
				// The key was never used anywhere, so a failed upload should
				// not leave a stray local half behind.
				if cleanupErr := sshx.RemoveStoredKey(name); cleanupErr != nil {
					ui.Warn("could not remove %s after the failed upload: %v", key.Path, cleanupErr)
				}
				return fmt.Errorf("uploading key %q to DigitalOcean: %w", name, err)
			}

			if jsonOutput() {
				return ui.JSON(map[string]any{"key": key, "digitalocean_id": created.ID})
			}

			fmt.Printf("Created SSH key %q\n", name)
			fmt.Printf("  fingerprint:  %s\n", key.Fingerprint)
			fmt.Printf("  private key:  %s\n", key.Path)
			fmt.Printf("  DigitalOcean: uploaded as key %d\n\n", created.ID)
			fmt.Printf("Reference it from a spec with:\n\n  defaults:\n    ssh_keys: [%s]\n\n", name)
			fmt.Println("Deploys find keys in the store automatically; no --ssh-key needed.")
			return nil
		},
	}
}

// ensureAccountKeyNameFree fails when the account already has a key with the
// given name.
func ensureAccountKeyNameFree(ctx context.Context, client *godo.Client, name string) error {
	keys, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Key, *godo.Response, error) {
		return client.Keys.List(ctx, opt)
	})
	if err != nil {
		return fmt.Errorf("listing SSH keys: %w", err)
	}
	for _, k := range keys {
		if k.Name == name {
			return fmt.Errorf("the account already has an SSH key named %q (id %d); pick another name", name, k.ID)
		}
	}
	return nil
}

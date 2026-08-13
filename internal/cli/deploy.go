package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/deploy"
	"github.com/jeremyaboyd/doploy/internal/pricing"
	"github.com/jeremyaboyd/doploy/internal/provision"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var (
		file           string
		dryRun         bool
		only           []string
		sshKey         string
		sshPort        int
		noBootstrap    bool
		skipSetup      bool
		wait           bool
		prune          bool
		insecure       bool
		assumeYes      bool
		connectTimeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Provision infrastructure and deploy the application",
		Long: `Reads the spec, creates any droplets that do not exist yet, then connects
over SSH and brings the services up with docker compose.

Running deploy again is safe: existing droplets are reused, and only the
containers are updated. doploy keeps no local state file -- it finds the
resources it owns through DigitalOcean tags, so any machine with a token can
reconcile the same deployment.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}

			path, err := spec.Find(file)
			if err != nil {
				return err
			}
			s, err := spec.Load(path)
			if err != nil {
				return err
			}

			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			opts := deploy.Options{
				SSHKeyPath:      sshKey,
				SSHPort:         sshPort,
				Bootstrap:       !noBootstrap,
				SkipSetup:       skipSetup,
				Wait:            wait,
				Prune:           prune,
				InsecureHostKey: insecure,
				Only:            only,
				DryRun:          dryRun,
				ConnectTimeout:  connectTimeout,
			}

			if insecure {
				ui.Warn("--insecure-host-key disables host key verification for this run")
			}

			// Creating droplets costs real money, so confirm before the first
			// one is created. Re-deploys onto existing droplets skip this.
			if !dryRun && !assumeYes {
				proceed, err := confirmNewResources(ctx, s, client)
				if err != nil {
					return err
				}
				if !proceed {
					return errors.New("aborted")
				}
			}

			d := &deploy.Deployer{Client: client, Spec: s, Opts: opts}
			summaries, err := d.Run(ctx)
			if err != nil {
				return err
			}

			if jsonOutput() {
				return ui.JSON(summaries)
			}
			printSummaries(summaries, dryRun)
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&file, "file", "f", "", "path to the spec file (default: doploy.yml)")
	flags.BoolVar(&dryRun, "dry-run", false, "show what would change without creating or deploying anything")
	flags.StringSliceVar(&only, "droplet", nil, "limit the deploy to these droplets (repeatable)")
	flags.StringVar(&sshKey, "ssh-key", "", "private key to connect with (default: the first key found in ~/.ssh)")
	flags.IntVar(&sshPort, "ssh-port", 22, "SSH port on the droplets")
	flags.BoolVar(&noBootstrap, "no-bootstrap", false, "skip installing Docker; assume the image already has it")
	flags.BoolVar(&skipSetup, "skip-setup", false, "skip host-level setup blocks; deploy containers only")
	flags.BoolVar(&wait, "wait", false, "wait for container healthchecks to pass before reporting success")
	flags.BoolVar(&prune, "prune", false, "remove dangling images on the droplet after deploying")
	flags.BoolVar(&insecure, "insecure-host-key", false, "skip SSH host key verification")
	flags.BoolVarP(&assumeYes, "yes", "y", false, "do not prompt before creating billable resources")
	flags.DurationVar(&connectTimeout, "connect-timeout", 5*time.Minute, "how long to wait for sshd on a new droplet")

	return cmd
}

// confirmNewResources plans the reconcile and, when it would create droplets,
// shows the cost and asks before going ahead.
//
// A redeploy onto droplets that already exist adds no recurring charge, so it
// proceeds without a prompt; only the first run for a given spec stops to ask.
func confirmNewResources(ctx context.Context, s *spec.Spec, client *godo.Client) (bool, error) {
	ui.Step("Planning")
	planner := &provision.Provisioner{Client: client, Spec: s, DryRun: true}
	plan, err := planner.Reconcile(ctx)
	if err != nil {
		return false, err
	}

	var creating []string
	for _, d := range plan.Droplets {
		if d.Created {
			creating = append(creating, d.Name)
		}
	}
	if len(creating) == 0 {
		return true, nil
	}

	sizes, err := fetchSizeIndex(ctx, client)
	if err != nil {
		return false, err
	}
	estimate := pricing.Calculate(s, sizes)

	fmt.Printf("\nThis creates %d new droplet(s): %s\n", len(creating), joinOrDash(creating))
	fmt.Printf("Estimated cost of the full deployment: %s/month (%s/hour)\n\n",
		ui.Money(estimate.TotalMonthly), ui.Money(estimate.TotalHourly))

	return confirm("Proceed?")
}

func printSummaries(summaries []deploy.Summary, dryRun bool) {
	if dryRun {
		fmt.Println("\nPlan (nothing was changed):")
	} else {
		fmt.Println("\nDeployed:")
	}

	table := ui.NewTable("DROPLET", "PUBLIC IP", "CREATED", "SERVICES", "STATUS")
	for _, s := range summaries {
		table.Row(s.Droplet, dash(s.PublicIP), s.Created, joinOrDash(s.Services), dash(s.Status))
	}
	table.Print()
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	out := items[0]
	for _, item := range items[1:] {
		out += "," + item
	}
	return out
}

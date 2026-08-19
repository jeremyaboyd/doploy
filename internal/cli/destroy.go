package cli

import (
	"errors"
	"fmt"

	"github.com/jeremyaboyd/doploy/internal/provision"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"github.com/spf13/cobra"
)

func newDestroyCmd() *cobra.Command {
	var (
		file        string
		project     string
		withVolumes bool
		assumeYes   bool
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down everything a deployment created",
		Long: `Deletes the droplets, firewalls, and tags belonging to a project, found
through the same ownership tags deploy uses -- so it removes exactly what
deploy manages, including droplets a stale spec no longer mentions. A records
pointing at the deleted droplets are removed too; the DNS zones stay.

Volumes are KEPT by default, because they are where the data lives. Pass
--volumes to delete them too; that is unrecoverable.

The project is read from the spec file, or named directly with --project when
the spec is no longer around.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}

			// --project works without a spec file: teardown must not require
			// the file that described what is being torn down.
			if project == "" {
				path, err := spec.Find(file)
				if err != nil {
					return fmt.Errorf("%w (or name the project directly with --project)", err)
				}
				s, err := spec.Load(path)
				if err != nil {
					return err
				}
				project = s.Project
			}

			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			d := &provision.Destroyer{Client: client, Project: project}

			ui.Step("Finding resources tagged %s", provision.ProjectTag(project))
			doomed, err := d.Plan(ctx)
			if err != nil {
				return err
			}

			if doomed.Empty() {
				fmt.Printf("Nothing is tagged as belonging to project %q.\n", project)
				return nil
			}

			printDoomed(doomed, withVolumes)

			if dryRun {
				fmt.Println("\nDry run: nothing was deleted.")
				return nil
			}

			if !assumeYes {
				prompt := fmt.Sprintf("Delete these %d resources?", countDoomed(doomed, withVolumes))
				if withVolumes && len(doomed.Volumes) > 0 {
					prompt = fmt.Sprintf("Delete these %d resources, INCLUDING the volumes and all data on them?", countDoomed(doomed, withVolumes))
				}
				proceed, err := confirm(prompt)
				if err != nil {
					return err
				}
				if !proceed {
					return errors.New("aborted")
				}
			}

			ui.Step("Destroying project %s", project)
			if err := d.Destroy(ctx, doomed, !withVolumes); err != nil {
				return err
			}

			fmt.Println("\nDone.")
			if !withVolumes && len(doomed.Volumes) > 0 {
				fmt.Printf("%d volume(s) were kept; rerun with --volumes to delete them and their data.\n", len(doomed.Volumes))
			}
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&file, "file", "f", "", "path to the spec file (default: doploy.yml)")
	flags.StringVar(&project, "project", "", "project name; skips reading a spec file")
	flags.BoolVar(&withVolumes, "volumes", false, "also delete block storage volumes and ALL DATA on them")
	flags.BoolVarP(&assumeYes, "yes", "y", false, "do not prompt for confirmation")
	flags.BoolVar(&dryRun, "dry-run", false, "show what would be deleted without deleting anything")

	return cmd
}

func printDoomed(doomed *provision.Doomed, withVolumes bool) {
	fmt.Println()
	table := ui.NewTable("KIND", "NAME", "DETAIL", "FATE")

	for _, d := range doomed.Droplets {
		publicIP, _ := d.PublicIPv4()
		detail := dash(publicIP)
		if d.Region != nil && d.Size != nil {
			detail = fmt.Sprintf("%s / %s / %s", d.Size.Slug, d.Region.Slug, dash(publicIP))
		}
		table.Row("droplet", d.Name, detail, "delete")
	}
	for _, v := range doomed.Volumes {
		fate := "KEEP (pass --volumes to delete)"
		if withVolumes {
			fate = "delete, WITH ALL DATA"
		}
		table.Row("volume", v.Name, fmt.Sprintf("%d GiB", v.SizeGigaBytes), fate)
	}
	for _, fw := range doomed.Firewalls {
		table.Row("firewall", fw.Name, "", "delete")
	}
	for _, r := range doomed.Records {
		table.Row("dns record", r.FQDN(), "A -> "+r.Record.Data, "delete (zone is kept)")
	}
	for _, tag := range doomed.Tags {
		table.Row("tag", tag, "", "delete")
	}
	table.Print()
}

func countDoomed(doomed *provision.Doomed, withVolumes bool) int {
	n := len(doomed.Droplets) + len(doomed.Firewalls) + len(doomed.Records)
	if withVolumes {
		n += len(doomed.Volumes)
	}
	return n
}

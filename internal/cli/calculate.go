package cli

import (
	"context"
	"fmt"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/pricing"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"github.com/spf13/cobra"
)

func newCalculateCmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:     "calculate",
		Aliases: []string{"calc", "cost"},
		Short:   "Estimate the monthly cost of the configured deployment",
		Long: `Estimates what the spec would cost to run for a month.

Droplet prices are read live from the API. Block storage and backups use
DigitalOcean's published list rates. Bandwidth overages, snapshots, and
anything provisioned outside the spec are not included.`,
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

			sizes, err := fetchSizeIndex(cmd.Context(), client)
			if err != nil {
				return err
			}

			estimate := pricing.Calculate(s, sizes)

			if jsonOutput() {
				return ui.JSON(estimate)
			}
			printEstimate(estimate, s)
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "path to the spec file (default: doploy.yml)")
	return cmd
}

// fetchSizeIndex loads the account's size catalogue for pricing lookups.
func fetchSizeIndex(ctx context.Context, client *godo.Client) (pricing.SizeIndex, error) {
	sizes, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Size, *godo.Response, error) {
		return client.Sizes.List(ctx, opt)
	})
	if err != nil {
		return nil, fmt.Errorf("listing sizes for pricing: %w", err)
	}
	return pricing.IndexSizes(sizes), nil
}

func printEstimate(est *pricing.Estimate, s *spec.Spec) {
	fmt.Printf("Project %s (%s)\n\n", est.Project, s.Path)

	table := ui.NewTable("KIND", "NAME", "DETAIL", "MONTHLY", "HOURLY")
	for _, item := range est.Items {
		table.Row(item.Kind, item.Name, item.Detail, ui.Money(item.Monthly), ui.Money(item.Hourly))
	}
	table.Print()

	fmt.Printf("\nEstimated total: %s/month  (%s/hour)\n",
		ui.Money(est.TotalMonthly), ui.Money(est.TotalHourly))

	for _, w := range est.Warnings {
		ui.Warn("%s", w)
	}

	fmt.Println("\nExcludes bandwidth overages, snapshots, and resources not described by the spec.")
}

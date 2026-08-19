package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/sshx"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List resources available in your DigitalOcean account",
	}
	cmd.AddCommand(
		newListDropletsCmd(),
		newListOSesCmd(),
		newListImagesCmd(),
		newListSizesCmd(),
		newListRegionsCmd(),
		newListDNSCmd(),
		newListSSHCmd(),
	)
	return cmd
}

func newListSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ssh [local]",
		Aliases: []string{"keys"},
		Short:   "List SSH keys on the account, or doploy's local keys and trusted hosts",
		Long: `Lists the SSH public keys on the DigitalOcean account -- the names that
` + "`ssh_keys:`" + ` in a spec can reference -- and marks the ones whose private key
is in doploy's local key store.

With "local", lists the store itself (keys created by ` + "`doploy add ssh`" + `) and
the droplet host keys doploy has recorded in its known_hosts file.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			if len(args) == 1 {
				if args[0] != "local" {
					return fmt.Errorf("unknown argument %q: the only argument `list ssh` takes is \"local\"", args[0])
				}
				return listLocalSSH()
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			return listAccountSSH(cmd.Context(), client)
		},
	}
}

func listAccountSSH(ctx context.Context, client *godo.Client) error {
	keys, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Key, *godo.Response, error) {
		return client.Keys.List(ctx, opt)
	})
	if err != nil {
		return fmt.Errorf("listing SSH keys: %w", err)
	}

	stored, err := sshx.ListStoredKeys()
	if err != nil {
		return err
	}
	// The API reports the legacy MD5 fingerprint, so match on that.
	local := map[string]bool{}
	for _, k := range stored {
		local[k.FingerprintMD5] = true
	}

	if jsonOutput() {
		return ui.JSON(keys)
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })

	table := ui.NewTable("NAME", "ID", "FINGERPRINT", "LOCAL")
	for _, k := range keys {
		managed := "-"
		if local[k.Fingerprint] {
			managed = "yes"
		}
		table.Row(k.Name, k.ID, k.Fingerprint, managed)
	}
	table.Print()

	if table.Len() == 0 {
		fmt.Println("\nNo SSH keys on this account. Create one with `doploy add ssh`.")
	}
	return nil
}

func listLocalSSH() error {
	stored, err := sshx.ListStoredKeys()
	if err != nil {
		return err
	}
	trusted, err := sshx.TrustedHosts()
	if err != nil {
		return err
	}

	if jsonOutput() {
		return ui.JSON(map[string]any{"keys": stored, "trusted_hosts": trusted})
	}

	fmt.Println("Local keys (doploy's key store):")
	keyTable := ui.NewTable("NAME", "FINGERPRINT", "PATH")
	for _, k := range stored {
		keyTable.Row(k.Name, dash(k.Fingerprint), k.Path)
	}
	keyTable.Print()
	if keyTable.Len() == 0 {
		fmt.Println("  (none; create one with `doploy add ssh`)")
	}

	fmt.Println("\nTrusted hosts (recorded on first connection):")
	hostTable := ui.NewTable("HOST", "TYPE", "FINGERPRINT")
	for _, h := range trusted {
		hostTable.Row(strings.Join(h.Hosts, ","), h.Type, h.Fingerprint)
	}
	hostTable.Print()
	if hostTable.Len() == 0 {
		fmt.Println("  (none yet; hosts are recorded the first time doploy connects)")
	}
	return nil
}

func newListDNSCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "dns [domain]",
		Aliases: []string{"domains", "zones"},
		Short:   "List DNS zones, or the records in one zone",
		Long: `Without an argument, lists the DNS zones on the account. With a domain,
lists every record in that zone -- including ones doploy did not create, since
a zone routinely carries MX, TXT, and other records alongside the A records
doploy manages.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			if len(args) == 0 {
				return listZones(ctx, client)
			}
			return listZoneRecords(ctx, client, args[0])
		},
	}
}

func listZones(ctx context.Context, client *godo.Client) error {
	domains, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Domain, *godo.Response, error) {
		return client.Domains.List(ctx, opt)
	})
	if err != nil {
		return fmt.Errorf("listing domains: %w", err)
	}

	if jsonOutput() {
		return ui.JSON(domains)
	}

	sort.Slice(domains, func(i, j int) bool { return domains[i].Name < domains[j].Name })

	table := ui.NewTable("ZONE", "TTL")
	for _, d := range domains {
		table.Row(d.Name, d.TTL)
	}
	table.Print()

	if table.Len() == 0 {
		fmt.Println("\nNo DNS zones on this account.")
	}
	return nil
}

func listZoneRecords(ctx context.Context, client *godo.Client, zone string) error {
	records, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.DomainRecord, *godo.Response, error) {
		return client.Domains.Records(ctx, zone, opt)
	})
	if err != nil {
		return fmt.Errorf("listing records in zone %s: %w", zone, err)
	}

	if jsonOutput() {
		return ui.JSON(records)
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].Type != records[j].Type {
			return records[i].Type < records[j].Type
		}
		return records[i].Name < records[j].Name
	})

	table := ui.NewTable("TYPE", "NAME", "DATA", "TTL")
	for _, r := range records {
		table.Row(r.Type, r.Name, r.Data, r.TTL)
	}
	table.Print()
	return nil
}

func newListDropletsCmd() *cobra.Command {
	var tag string

	cmd := &cobra.Command{
		Use:     "droplets",
		Aliases: []string{"droplet"},
		Short:   "List your droplets",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			droplets, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
				if tag != "" {
					return client.Droplets.ListByTag(ctx, tag, opt)
				}
				return client.Droplets.List(ctx, opt)
			})
			if err != nil {
				return fmt.Errorf("listing droplets: %w", err)
			}

			if jsonOutput() {
				return ui.JSON(droplets)
			}

			table := ui.NewTable("ID", "NAME", "STATUS", "REGION", "SIZE", "PUBLIC IP", "MONTHLY", "TAGS")
			for i := range droplets {
				d := &droplets[i]
				publicIP, _ := d.PublicIPv4()

				var region, size, price string
				if d.Region != nil {
					region = d.Region.Slug
				}
				if d.Size != nil {
					size = d.Size.Slug
					price = ui.Money(d.Size.PriceMonthly)
				}
				table.Row(d.ID, d.Name, d.Status, dash(region), dash(size), dash(publicIP), dash(price), dash(strings.Join(d.Tags, ",")))
			}
			table.Print()

			if table.Len() == 0 {
				fmt.Println("\nNo droplets found.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&tag, "tag", "", "only droplets carrying this tag")
	return cmd
}

func newListOSesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "oses",
		Aliases: []string{"os", "distributions"},
		Short:   "List distribution images you can boot a droplet from",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			images, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Image, *godo.Response, error) {
				return client.Images.ListDistribution(ctx, opt)
			})
			if err != nil {
				return fmt.Errorf("listing distributions: %w", err)
			}

			sort.Slice(images, func(i, j int) bool {
				if images[i].Distribution != images[j].Distribution {
					return images[i].Distribution < images[j].Distribution
				}
				return images[i].Slug < images[j].Slug
			})

			if jsonOutput() {
				return ui.JSON(images)
			}

			table := ui.NewTable("SLUG", "DISTRIBUTION", "NAME", "MIN DISK")
			for _, img := range images {
				// Images without a slug cannot be referenced from a spec, so
				// listing them would only be misleading.
				if img.Slug == "" {
					continue
				}
				table.Row(img.Slug, img.Distribution, img.Name, fmt.Sprintf("%d GB", img.MinDiskSize))
			}
			table.Print()
			return nil
		},
	}
}

func newListImagesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "images",
		Aliases: []string{"image", "1-clicks", "oneclicks"},
		Short:   "List 1-Click marketplace images",
		Long: `Lists the 1-Click applications available for droplets.

Any slug shown here can be used as an ` + "`image:`" + ` value in doploy.yml. The
docker 1-Click is a good starting point: it boots with Docker and the compose
plugin already installed, so doploy skips the bootstrap step entirely.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			oneClicks, _, err := client.OneClick.List(ctx, "droplet")
			if err != nil {
				return fmt.Errorf("listing 1-Click images: %w", err)
			}

			if jsonOutput() {
				return ui.JSON(oneClicks)
			}

			// Application images carry the human-readable names that the
			// 1-Click endpoint omits, so join the two on slug where possible.
			names := applicationImageNames(ctx, client)

			sort.Slice(oneClicks, func(i, j int) bool { return oneClicks[i].Slug < oneClicks[j].Slug })

			table := ui.NewTable("SLUG", "NAME", "TYPE")
			for _, oc := range oneClicks {
				table.Row(oc.Slug, dash(names[oc.Slug]), oc.Type)
			}
			table.Print()
			return nil
		},
	}
}

// applicationImageNames maps image slugs to display names, best-effort. A
// failure here only costs a column, so it is swallowed rather than surfaced.
func applicationImageNames(ctx context.Context, client *godo.Client) map[string]string {
	images, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Image, *godo.Response, error) {
		return client.Images.ListApplication(ctx, opt)
	})
	if err != nil {
		return map[string]string{}
	}

	names := make(map[string]string, len(images))
	for _, img := range images {
		if img.Slug != "" {
			names[img.Slug] = img.Name
		}
	}
	return names
}

func newListSizesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sizes [region]",
		Aliases: []string{"size"},
		Short:   "List droplet sizes, optionally filtered to a region",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			var region string
			if len(args) == 1 {
				region = args[0]
			}

			sizes, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Size, *godo.Response, error) {
				return client.Sizes.List(ctx, opt)
			})
			if err != nil {
				return fmt.Errorf("listing sizes: %w", err)
			}

			var filtered []godo.Size
			for _, s := range sizes {
				if region == "" || containsString(s.Regions, region) {
					filtered = append(filtered, s)
				}
			}

			if region != "" && len(filtered) == 0 {
				return fmt.Errorf("no sizes available in region %q (run `doploy list regions` to see valid slugs)", region)
			}

			sort.Slice(filtered, func(i, j int) bool { return filtered[i].PriceMonthly < filtered[j].PriceMonthly })

			if jsonOutput() {
				return ui.JSON(filtered)
			}

			header := "SLUG\tVCPUS\tMEMORY\tDISK\tTRANSFER\tMONTHLY\tHOURLY\tAVAILABLE"
			table := ui.NewTable(strings.Split(header, "\t")...)
			for _, s := range filtered {
				table.Row(
					s.Slug,
					s.Vcpus,
					fmt.Sprintf("%d GB", s.Memory/1024),
					fmt.Sprintf("%d GB", s.Disk),
					fmt.Sprintf("%.0f TB", s.Transfer),
					ui.Money(s.PriceMonthly),
					ui.Money(s.PriceHourly),
					s.Available,
				)
			}
			table.Print()
			return nil
		},
	}
}

func newListRegionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "regions",
		Aliases: []string{"region"},
		Short:   "List deployment regions",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutput(); err != nil {
				return err
			}
			client, err := apiClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			regions, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Region, *godo.Response, error) {
				return client.Regions.List(ctx, opt)
			})
			if err != nil {
				return fmt.Errorf("listing regions: %w", err)
			}

			sort.Slice(regions, func(i, j int) bool { return regions[i].Slug < regions[j].Slug })

			if jsonOutput() {
				return ui.JSON(regions)
			}

			table := ui.NewTable("SLUG", "NAME", "AVAILABLE", "SIZES", "FEATURES")
			for _, r := range regions {
				table.Row(r.Slug, r.Name, r.Available, len(r.Sizes), strings.Join(r.Features, ","))
			}
			table.Print()
			return nil
		},
	}
}

func containsString(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// dash renders an empty value as a placeholder so columns never look truncated.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

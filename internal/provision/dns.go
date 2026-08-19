package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/ui"
)

// dnsTTL is the TTL for records doploy creates, matching the DigitalOcean
// console default. Existing records keep whatever TTL they already have.
const dnsTTL = 1800

// ensureDNS points an A record at every droplet that resolved a DNS name,
// creating the zone first when the account does not have one.
//
// Records are upserted: a record that already points at the right address is
// left alone, and one pointing elsewhere is rewritten, which is what makes a
// redeploy after a droplet rebuild heal the DNS too.
func (p *Provisioner) ensureDNS(ctx context.Context, result *Result) error {
	var targets []*DropletState
	for _, state := range result.Droplets {
		if state.Spec.DNSName() != "" {
			targets = append(targets, state)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	domains, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Domain, *godo.Response, error) {
		return p.Client.Domains.List(ctx, opt)
	})
	if err != nil {
		return fmt.Errorf("listing domains: %w", err)
	}
	zones := make([]string, len(domains))
	for i, d := range domains {
		zones[i] = d.Name
	}

	for _, state := range targets {
		fqdn := state.Spec.DNSName()
		if state.PublicIP == "" {
			return fmt.Errorf("droplet %q: cannot point %s at it, it has no public IPv4 address", state.Name, fqdn)
		}

		zone, record, exists := zoneFor(fqdn, zones, p.Spec.Defaults.DNS)
		if !exists {
			ui.Substep("dns: creating zone %s", zone)
			if _, _, err := p.Client.Domains.Create(ctx, &godo.DomainCreateRequest{Name: zone}); err != nil {
				return fmt.Errorf("creating zone %s: %w", zone, err)
			}
			// Later droplets in this run should find the zone we just made.
			zones = append(zones, zone)
		}

		if err := p.upsertARecord(ctx, zone, record, fqdn, state); err != nil {
			return fmt.Errorf("droplet %q dns: %w", state.Name, err)
		}
	}
	return nil
}

// upsertARecord makes the A record for `record` in `zone` point at the
// droplet's public IP.
func (p *Provisioner) upsertARecord(ctx context.Context, zone, record, fqdn string, state *DropletState) error {
	records, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.DomainRecord, *godo.Response, error) {
		return p.Client.Domains.Records(ctx, zone, opt)
	})
	if err != nil {
		return fmt.Errorf("listing records in zone %s: %w", zone, err)
	}

	var existing []godo.DomainRecord
	for _, r := range records {
		if r.Type == "A" && r.Name == record {
			existing = append(existing, r)
		}
	}

	if len(existing) == 0 {
		ui.Substep("dns: creating A record %s -> %s", fqdn, state.PublicIP)
		_, _, err := p.Client.Domains.CreateRecord(ctx, zone, &godo.DomainRecordEditRequest{
			Type: "A",
			Name: record,
			Data: state.PublicIP,
			TTL:  dnsTTL,
		})
		return err
	}

	// Several A records at one name is round-robin, which doploy never sets up
	// itself; converge the first and leave the rest to whoever made them.
	if len(existing) > 1 {
		ui.Warn("dns: %s has %d A records; doploy manages only one of them", fqdn, len(existing))
	}

	if existing[0].Data == state.PublicIP {
		ui.Substep("dns: A record %s already points at %s", fqdn, state.PublicIP)
		return nil
	}

	ui.Substep("dns: updating A record %s: %s -> %s", fqdn, existing[0].Data, state.PublicIP)
	_, _, err = p.Client.Domains.EditRecord(ctx, zone, existing[0].ID, &godo.DomainRecordEditRequest{
		Type: "A",
		Name: record,
		Data: state.PublicIP,
	})
	return err
}

// zoneFor decides which zone a DNS name's record belongs in and what the
// record is called inside it ("@" for the zone apex).
//
// An existing zone on the account always wins, longest match first, so a
// record never lands in a parent zone when a delegated child zone exists.
// Otherwise the zone has to be created: the spec's defaults.dns is used when
// the name falls under it, and failing that the registrable domain is assumed
// to be the last two labels -- names under multi-part public suffixes
// (example.co.uk) need defaults.dns or a pre-created zone to land right.
func zoneFor(fqdn string, existing []string, hint string) (zone, record string, exists bool) {
	best := ""
	for _, z := range existing {
		if (fqdn == z || strings.HasSuffix(fqdn, "."+z)) && len(z) > len(best) {
			best = z
		}
	}
	if best != "" {
		return best, recordIn(best, fqdn), true
	}

	if hint != "" && (fqdn == hint || strings.HasSuffix(fqdn, "."+hint)) {
		return hint, recordIn(hint, fqdn), false
	}

	labels := strings.Split(fqdn, ".")
	zone = strings.Join(labels[len(labels)-2:], ".")
	return zone, recordIn(zone, fqdn), false
}

// recordIn returns the record name for fqdn relative to zone.
func recordIn(zone, fqdn string) string {
	if fqdn == zone {
		return "@"
	}
	return strings.TrimSuffix(fqdn, "."+zone)
}

// fqdnOf reverses recordIn for display.
func fqdnOf(zone, record string) string {
	if record == "@" || record == "" {
		return zone
	}
	return record + "." + zone
}

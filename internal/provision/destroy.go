package provision

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/ui"
)

// Doomed is everything destroy found for a project. It is built first and shown
// to the user before anything is deleted, so the confirmation is over a
// concrete list rather than a promise.
type Doomed struct {
	Project   string          `json:"project"`
	Droplets  []godo.Droplet  `json:"droplets"`
	Volumes   []godo.Volume   `json:"volumes"`
	Firewalls []godo.Firewall `json:"firewalls"`
	Records   []DoomedRecord  `json:"dns_records"`
	Tags      []string        `json:"tags"`
}

// DoomedRecord is a DNS record about to point at a deleted droplet.
type DoomedRecord struct {
	Zone   string            `json:"zone"`
	Record godo.DomainRecord `json:"record"`
}

// FQDN is the record's full name, for display.
func (r *DoomedRecord) FQDN() string { return fqdnOf(r.Zone, r.Record.Name) }

// Empty reports whether there is nothing to delete.
func (d *Doomed) Empty() bool {
	return len(d.Droplets) == 0 && len(d.Volumes) == 0 && len(d.Firewalls) == 0 && len(d.Records) == 0
}

// Destroyer tears down a project's resources.
type Destroyer struct {
	Client  *godo.Client
	Project string
}

// firewallPrefix matches the names ensureFirewall creates.
func (d *Destroyer) firewallPrefix() string { return "doploy-" + d.Project + "-" }

// Plan finds everything tagged as belonging to the project.
//
// Discovery goes through the same tags that deploy uses to find its resources,
// so destroy tears down exactly what deploy would have reused -- including
// droplets a stale spec no longer mentions. Firewalls carry no per-project tag
// (the API attaches them by droplet ID), so those are matched by the name
// prefix doploy gives them.
func (d *Destroyer) Plan(ctx context.Context) (*Doomed, error) {
	tag := ProjectTag(d.Project)
	doomed := &Doomed{Project: d.Project}

	droplets, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
		return d.Client.Droplets.ListByTag(ctx, tag, opt)
	})
	if err != nil {
		return nil, fmt.Errorf("listing droplets tagged %s: %w", tag, err)
	}
	doomed.Droplets = droplets

	volumes, _, err := d.Client.Storage.ListVolumes(ctx, &godo.ListVolumeParams{
		ListOptions: &godo.ListOptions{PerPage: 200},
	})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	for _, v := range volumes {
		if hasTag(v.Tags, tag) {
			doomed.Volumes = append(doomed.Volumes, v)
		}
	}

	firewalls, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Firewall, *godo.Response, error) {
		return d.Client.Firewalls.List(ctx, opt)
	})
	if err != nil {
		return nil, fmt.Errorf("listing firewalls: %w", err)
	}
	for _, fw := range firewalls {
		if strings.HasPrefix(fw.Name, d.firewallPrefix()) {
			doomed.Firewalls = append(doomed.Firewalls, fw)
		}
	}

	records, err := d.findRecords(ctx, doomed.Droplets)
	if err != nil {
		return nil, err
	}
	doomed.Records = records

	// The project tag plus one ownership tag per droplet. The generic "doploy"
	// marker is shared by every project and is left alone.
	doomed.Tags = append(doomed.Tags, tag)
	for _, droplet := range doomed.Droplets {
		if name, ok := dropletNameFromTags(d.Project, droplet.Tags); ok {
			doomed.Tags = append(doomed.Tags, DropletTag(d.Project, name))
		}
	}

	return doomed, nil
}

// findRecords scans the account's DNS zones for A records pointing at the
// doomed droplets' public addresses.
//
// DNS records cannot carry tags, so unlike everything else destroy discovers
// they are matched by where they point: a record aimed at a droplet being
// deleted is about to dangle regardless of who created it. Zones themselves
// are never deleted -- they routinely hold MX and other records doploy did not
// create.
func (d *Destroyer) findRecords(ctx context.Context, droplets []godo.Droplet) ([]DoomedRecord, error) {
	ips := map[string]bool{}
	for _, droplet := range droplets {
		if ip, err := droplet.PublicIPv4(); err == nil && ip != "" {
			ips[ip] = true
		}
	}
	if len(ips) == 0 {
		return nil, nil
	}

	domains, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Domain, *godo.Response, error) {
		return d.Client.Domains.List(ctx, opt)
	})
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}

	var doomed []DoomedRecord
	for _, domain := range domains {
		records, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.DomainRecord, *godo.Response, error) {
			return d.Client.Domains.RecordsByType(ctx, domain.Name, "A", opt)
		})
		if err != nil {
			return nil, fmt.Errorf("listing records in zone %s: %w", domain.Name, err)
		}
		for _, r := range records {
			if ips[r.Data] {
				doomed = append(doomed, DoomedRecord{Zone: domain.Name, Record: r})
			}
		}
	}
	return doomed, nil
}

// Destroy deletes what Plan found: DNS records first (stop pointing names at
// hosts about to die), then droplets (which detaches their volumes), then
// volumes unless kept, then firewalls, then the project's tags.
func (d *Destroyer) Destroy(ctx context.Context, doomed *Doomed, keepVolumes bool) error {
	for _, r := range doomed.Records {
		ui.Substep("deleting A record %s -> %s", r.FQDN(), r.Record.Data)
		if _, err := d.Client.Domains.DeleteRecord(ctx, r.Zone, r.Record.ID); err != nil {
			return fmt.Errorf("deleting A record %s: %w", r.FQDN(), err)
		}
	}

	for _, droplet := range doomed.Droplets {
		ui.Substep("deleting droplet %s (%d)", droplet.Name, droplet.ID)
		if _, err := d.Client.Droplets.Delete(ctx, droplet.ID); err != nil {
			return fmt.Errorf("deleting droplet %d: %w", droplet.ID, err)
		}
	}

	if !keepVolumes {
		for _, volume := range doomed.Volumes {
			// A volume cannot be deleted while attached, and droplet deletion
			// detaches asynchronously, so wait for the attachment to clear.
			if err := d.waitForDetach(ctx, volume.ID); err != nil {
				return err
			}
			ui.Substep("deleting volume %s (%d GiB)", volume.Name, volume.SizeGigaBytes)
			if _, err := d.Client.Storage.DeleteVolume(ctx, volume.ID); err != nil {
				return fmt.Errorf("deleting volume %s: %w", volume.Name, err)
			}
		}
	} else {
		for _, volume := range doomed.Volumes {
			ui.Substep("keeping volume %s (%d GiB); delete it later with --volumes or in the console", volume.Name, volume.SizeGigaBytes)
		}
	}

	for _, fw := range doomed.Firewalls {
		ui.Substep("deleting firewall %s", fw.Name)
		if _, err := d.Client.Firewalls.Delete(ctx, fw.ID); err != nil {
			return fmt.Errorf("deleting firewall %s: %w", fw.Name, err)
		}
	}

	for _, tag := range doomed.Tags {
		// Best-effort: a tag that is already gone, or still referenced by a
		// kept volume, is not worth failing the teardown over.
		if _, err := d.Client.Tags.Delete(ctx, tag); err == nil {
			ui.Substep("deleted tag %s", tag)
		}
	}

	return nil
}

// waitForDetach polls until a volume has no attached droplets.
func (d *Destroyer) waitForDetach(ctx context.Context, volumeID string) error {
	deadline := time.Now().Add(actionTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		volume, _, err := d.Client.Storage.GetVolume(ctx, volumeID)
		if err != nil {
			return fmt.Errorf("polling volume %s: %w", volumeID, err)
		}
		if len(volume.DropletIDs) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("volume %s is still attached after %s", volume.Name, actionTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

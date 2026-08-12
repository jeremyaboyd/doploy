package provision

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/ui"
)

// How long to wait for a freshly created droplet to become active and get an
// address, and how often to check.
const (
	activeTimeout = 10 * time.Minute
	pollInterval  = 5 * time.Second
	actionTimeout = 5 * time.Minute
)

// Provisioner reconciles a spec against the account.
type Provisioner struct {
	Client *godo.Client
	Spec   *spec.Spec

	// DryRun reports what would change without creating anything.
	DryRun bool
}

// DropletState is the outcome of reconciling one droplet from the spec.
type DropletState struct {
	Name      string        `json:"name"`
	Spec      *spec.Droplet `json:"-"`
	Droplet   *godo.Droplet `json:"-"`
	ID        int           `json:"id"`
	PublicIP  string        `json:"public_ip"`
	PrivateIP string        `json:"private_ip"`
	Created   bool          `json:"created"`
	Drift     []string      `json:"drift,omitempty"`
}

// Result is the full reconcile outcome.
type Result struct {
	Droplets []*DropletState `json:"droplets"`
}

// ByName returns the state for a spec droplet name.
func (r *Result) ByName(name string) *DropletState {
	for _, d := range r.Droplets {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// Reconcile brings the account in line with the spec: it creates droplets that
// do not exist, reuses those that do, then ensures volumes and firewalls.
func (p *Provisioner) Reconcile(ctx context.Context) (*Result, error) {
	existing, err := p.findExisting(ctx)
	if err != nil {
		return nil, err
	}

	sshKeys, err := p.resolveSSHKeys(ctx)
	if err != nil {
		return nil, err
	}

	result := &Result{}

	for _, name := range p.Spec.DropletNames() {
		desired := p.Spec.Droplets[name]

		if found, ok := existing[name]; ok {
			state := &DropletState{
				Name:    name,
				Spec:    desired,
				Droplet: found,
				ID:      found.ID,
				Created: false,
				Drift:   detectDrift(desired, found),
			}
			state.PublicIP, _ = found.PublicIPv4()
			state.PrivateIP, _ = found.PrivateIPv4()

			ui.Substep("droplet %s: reusing existing droplet %d (%s)", name, found.ID, state.PublicIP)
			for _, d := range state.Drift {
				ui.Warn("droplet %s: %s", name, d)
			}
			result.Droplets = append(result.Droplets, state)
			continue
		}

		if p.DryRun {
			ui.Substep("droplet %s: would create %s in %s from image %s", name, desired.Size, desired.Region, desired.Image)
			result.Droplets = append(result.Droplets, &DropletState{Name: name, Spec: desired, Created: true})
			continue
		}

		created, err := p.createDroplet(ctx, desired, sshKeys)
		if err != nil {
			return nil, fmt.Errorf("creating droplet %q: %w", name, err)
		}
		state := &DropletState{
			Name:    name,
			Spec:    desired,
			Droplet: created,
			ID:      created.ID,
			Created: true,
		}
		state.PublicIP, _ = created.PublicIPv4()
		state.PrivateIP, _ = created.PrivateIPv4()
		result.Droplets = append(result.Droplets, state)
	}

	if p.DryRun {
		p.reportPlannedExtras()
		return result, nil
	}

	if err := p.ensureVolumes(ctx, result); err != nil {
		return nil, err
	}
	if err := p.ensureFirewalls(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

// findExisting returns the droplets already tagged for this project, keyed by
// their spec name.
func (p *Provisioner) findExisting(ctx context.Context) (map[string]*godo.Droplet, error) {
	tag := ProjectTag(p.Spec.Project)

	droplets, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
		return p.Client.Droplets.ListByTag(ctx, tag, opt)
	})
	if err != nil {
		return nil, fmt.Errorf("listing droplets tagged %s: %w", tag, err)
	}

	found := map[string]*godo.Droplet{}
	for i := range droplets {
		d := &droplets[i]
		name, ok := dropletNameFromTags(p.Spec.Project, d.Tags)
		if !ok {
			continue
		}
		// A duplicate means something created two droplets with the same tag.
		// Keep the oldest (lowest ID) so behaviour is deterministic, and say so.
		if prior, dup := found[name]; dup {
			if prior.ID <= d.ID {
				ui.Warn("droplet %s: found duplicate droplet %d, using %d; clean this up manually", name, d.ID, prior.ID)
				continue
			}
			ui.Warn("droplet %s: found duplicate droplet %d, using %d; clean this up manually", name, prior.ID, d.ID)
		}
		found[name] = d
	}
	return found, nil
}

// createDroplet creates one droplet and waits for it to become reachable.
func (p *Provisioner) createDroplet(ctx context.Context, d *spec.Droplet, sshKeys []godo.DropletCreateSSHKey) (*godo.Droplet, error) {
	tags := tagsFor(p.Spec.Project, d.Name, d.Tags)
	if err := ensureTags(ctx, p.Client, tags); err != nil {
		return nil, err
	}

	// PrivateNetworking is deliberately not set: it is deprecated, and droplets
	// now join the region's default VPC automatically unless VPCUUID says
	// otherwise.
	req := &godo.DropletCreateRequest{
		Name:       p.Spec.Project + "-" + d.Name,
		Region:     d.Region,
		Size:       d.Size,
		Image:      imageFor(d.Image),
		SSHKeys:    sshKeys,
		Backups:    spec.BoolOr(d.Backups, false),
		IPv6:       spec.BoolOr(d.IPv6, false),
		Monitoring: spec.BoolOr(d.Monitoring, true),
		Tags:       tags,
		UserData:   d.UserData,
	}
	if d.VPCUUID != "" {
		req.VPCUUID = d.VPCUUID
	}

	ui.Substep("droplet %s: creating %s in %s from %s", d.Name, d.Size, d.Region, d.Image)
	created, _, err := p.Client.Droplets.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	return p.waitForActive(ctx, created.ID, d.Name)
}

// waitForActive polls until the droplet is active and has a public address.
func (p *Provisioner) waitForActive(ctx context.Context, id int, name string) (*godo.Droplet, error) {
	deadline := time.Now().Add(activeTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		droplet, _, err := p.Client.Droplets.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("polling droplet %d: %w", id, err)
		}

		if droplet.Status == "active" {
			if ip, err := droplet.PublicIPv4(); err == nil && ip != "" {
				ui.Substep("droplet %s: active at %s", name, ip)
				return droplet, nil
			}
		}
		if droplet.Status == "archive" {
			return nil, fmt.Errorf("droplet %d entered status %q", id, droplet.Status)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("droplet %d did not become active within %s (last status: %s)", id, activeTimeout, droplet.Status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// imageFor accepts either a numeric image ID or a slug.
func imageFor(image string) godo.DropletCreateImage {
	if id, err := strconv.Atoi(image); err == nil {
		return godo.DropletCreateImage{ID: id}
	}
	return godo.DropletCreateImage{Slug: image}
}

// resolveSSHKeys turns the spec's key names, fingerprints, or IDs into the
// references the create API expects.
//
// Deploying requires SSH access, so a spec with no usable key is a hard error
// rather than a droplet nobody can reach.
func (p *Provisioner) resolveSSHKeys(ctx context.Context) ([]godo.DropletCreateSSHKey, error) {
	wanted := map[string]bool{}
	for _, d := range p.Spec.Droplets {
		for _, k := range d.SSHKeys {
			wanted[k] = true
		}
	}

	accountKeys, err := doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Key, *godo.Response, error) {
		return p.Client.Keys.List(ctx, opt)
	})
	if err != nil {
		return nil, fmt.Errorf("listing SSH keys on the account: %w", err)
	}
	if len(accountKeys) == 0 {
		return nil, errors.New("no SSH keys on this DigitalOcean account; add one at https://cloud.digitalocean.com/account/security before deploying")
	}

	// With nothing specified, fall back to the account's only key. Picking one
	// out of several would be a guess with real consequences, so that errors.
	if len(wanted) == 0 {
		if len(accountKeys) == 1 {
			ui.Substep("using the account's only SSH key %q", accountKeys[0].Name)
			return []godo.DropletCreateSSHKey{{ID: accountKeys[0].ID}}, nil
		}
		names := make([]string, len(accountKeys))
		for i, k := range accountKeys {
			names[i] = k.Name
		}
		return nil, fmt.Errorf("no `ssh_keys` set and the account has %d keys (%v); name the ones to install in `defaults.ssh_keys`", len(accountKeys), names)
	}

	var resolved []godo.DropletCreateSSHKey
	for _, want := range sortedStrings(wanted) {
		match := findKey(accountKeys, want)
		if match == nil {
			return nil, fmt.Errorf("SSH key %q is not on this DigitalOcean account (match by name, fingerprint, or ID)", want)
		}
		resolved = append(resolved, godo.DropletCreateSSHKey{ID: match.ID})
	}
	return resolved, nil
}

func findKey(keys []godo.Key, want string) *godo.Key {
	for i := range keys {
		k := &keys[i]
		if k.Name == want || k.Fingerprint == want || strconv.Itoa(k.ID) == want {
			return k
		}
	}
	return nil
}

// detectDrift compares a live droplet against the spec. doploy reports drift
// rather than acting on it: resizing needs a power cycle and changing region or
// image means rebuilding, both of which should be a deliberate choice.
func detectDrift(want *spec.Droplet, have *godo.Droplet) []string {
	var drift []string

	if have.Size != nil && have.Size.Slug != want.Size {
		drift = append(drift, fmt.Sprintf("size is %s but the spec says %s; resize in the console or destroy and redeploy", have.Size.Slug, want.Size))
	}
	if have.Region != nil && have.Region.Slug != want.Region {
		drift = append(drift, fmt.Sprintf("region is %s but the spec says %s; a droplet cannot be moved between regions", have.Region.Slug, want.Region))
	}
	return drift
}

// reportPlannedExtras lists the volumes and firewalls a dry run would create.
func (p *Provisioner) reportPlannedExtras() {
	for _, name := range p.Spec.DropletNames() {
		d := p.Spec.Droplets[name]
		for _, v := range d.Volumes {
			ui.Substep("droplet %s: would ensure %d GiB volume %q at %s", name, v.SizeGB, p.volumeName(v.Name), v.MountPathOrDefault())
		}
		if d.Firewall != nil {
			ui.Substep("droplet %s: would ensure firewall %q", name, p.firewallName(name))
		}
	}
}

func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	// Small sets; insertion sort keeps this dependency-free and stable.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

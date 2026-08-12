package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/doclient"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/ui"
)

// anywhere is the default source/destination for firewall rules.
var anywhere = []string{"0.0.0.0/0", "::/0"}

// sshPort must always be reachable: doploy deploys over SSH, so a firewall that
// omits it would lock the tool out of the droplet it just created.
const sshPort = "22"

func (p *Provisioner) firewallName(droplet string) string {
	return "doploy-" + p.Spec.Project + "-" + droplet
}

// ensureFirewalls creates or updates a cloud firewall for each droplet that
// declares one. Droplets without a `firewall:` block are left alone, so an
// externally managed firewall is never clobbered.
func (p *Provisioner) ensureFirewalls(ctx context.Context, result *Result) error {
	var existing []godo.Firewall
	var loaded bool

	for _, state := range result.Droplets {
		if state.Spec.Firewall == nil {
			continue
		}

		if !loaded {
			var err error
			existing, err = doclient.Paginate(func(opt *godo.ListOptions) ([]godo.Firewall, *godo.Response, error) {
				return p.Client.Firewalls.List(ctx, opt)
			})
			if err != nil {
				return fmt.Errorf("listing firewalls: %w", err)
			}
			loaded = true
		}

		if err := p.ensureFirewall(ctx, state, existing); err != nil {
			return fmt.Errorf("droplet %q firewall: %w", state.Name, err)
		}
	}
	return nil
}

func (p *Provisioner) ensureFirewall(ctx context.Context, state *DropletState, existing []godo.Firewall) error {
	name := p.firewallName(state.Name)
	req := &godo.FirewallRequest{
		Name:          name,
		InboundRules:  buildInboundRules(state.Spec.Firewall),
		OutboundRules: allowAllOutbound(),
		DropletIDs:    []int{state.ID},
		Tags:          []string{MarkerTag},
	}

	for i := range existing {
		if existing[i].Name != name {
			continue
		}
		ui.Substep("firewall %s: updating", name)
		_, _, err := p.Client.Firewalls.Update(ctx, existing[i].ID, req)
		return err
	}

	ui.Substep("firewall %s: creating (inbound: %s)", name, summarize(req.InboundRules))
	_, _, err := p.Client.Firewalls.Create(ctx, req)
	return err
}

// buildInboundRules translates the spec's port list into API rules, always
// including SSH.
func buildInboundRules(fw *spec.Firewall) []godo.InboundRule {
	sources := fw.Sources
	if len(sources) == 0 {
		sources = anywhere
	}

	sshSources := fw.SSHSources
	if len(sshSources) == 0 {
		sshSources = anywhere
	}

	rules := []godo.InboundRule{{
		Protocol:  "tcp",
		PortRange: sshPort,
		Sources:   &godo.Sources{Addresses: sshSources},
	}}

	for _, entry := range fw.Inbound {
		portPart, proto, hasProto := strings.Cut(entry, "/")
		if !hasProto {
			proto = "tcp"
		}
		// SSH is already covered above; a duplicate rule would be rejected.
		if portPart == sshPort && proto == "tcp" {
			continue
		}
		rules = append(rules, godo.InboundRule{
			Protocol:  proto,
			PortRange: portPart,
			Sources:   &godo.Sources{Addresses: sources},
		})
	}
	return rules
}

// allowAllOutbound permits all egress, which the droplet needs in order to pull
// images and install packages.
func allowAllOutbound() []godo.OutboundRule {
	dest := &godo.Destinations{Addresses: anywhere}
	return []godo.OutboundRule{
		{Protocol: "tcp", PortRange: "all", Destinations: dest},
		{Protocol: "udp", PortRange: "all", Destinations: dest},
		{Protocol: "icmp", Destinations: dest},
	}
}

func summarize(rules []godo.InboundRule) string {
	parts := make([]string, len(rules))
	for i, r := range rules {
		parts[i] = r.PortRange + "/" + r.Protocol
	}
	return strings.Join(parts, ", ")
}

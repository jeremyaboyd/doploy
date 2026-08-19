package spec

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// namePattern constrains project, droplet, and service names to what is safe in
// DigitalOcean tags, droplet names, and docker compose project names.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// portPattern matches "80", "8080:80", "127.0.0.1:8080:80", each with an
// optional /tcp or /udp suffix and optional ranges.
var portPattern = regexp.MustCompile(`^(\d[\d.]*:)?(\d+(-\d+)?:)?\d+(-\d+)?(/(tcp|udp))?$`)

// dnsLabelPattern is one label of a DNS name. Lowercase only, matching how the
// DigitalOcean API stores names.
var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Errors collects every validation failure so one run reports them all rather
// than making the user fix them one at a time.
type Errors []string

func (e Errors) Error() string {
	if len(e) == 1 {
		return e[0]
	}
	return fmt.Sprintf("%d problems:\n  - %s", len(e), strings.Join(e, "\n  - "))
}

// Validate checks a resolved spec for internal consistency. It does not contact
// the API, so unknown region or size slugs are caught later at provision time.
func (s *Spec) Validate() error {
	var errs Errors

	if s.Project == "" {
		errs = append(errs, "`project:` is required (it namespaces the tags doploy uses to find your resources)")
	} else if !namePattern.MatchString(s.Project) {
		errs = append(errs, fmt.Sprintf("project %q must be lowercase alphanumeric with dashes, starting with a letter or digit", s.Project))
	}

	if len(s.Services) == 0 {
		errs = append(errs, "no `services:` defined, so there is nothing to deploy")
	}

	errs = append(errs, s.validateDroplets()...)
	errs = append(errs, s.validateServices()...)
	errs = append(errs, s.validateDropletDependencies()...)
	errs = append(errs, s.ValidateRuntimeReferences()...)
	errs = append(errs, s.validateLocalPaths()...)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// validateDropletDependencies checks depends_on between droplets and rejects
// cycles, which would otherwise make deploy order undefined.
func (s *Spec) validateDropletDependencies() Errors {
	var errs Errors

	for _, name := range s.DropletNames() {
		for _, dep := range s.Droplets[name].DependsOn {
			if dep == name {
				errs = append(errs, fmt.Sprintf("droplet %q depends on itself", name))
				continue
			}
			if _, ok := s.Droplets[dep]; !ok {
				errs = append(errs, fmt.Sprintf("droplet %q depends on %q, which is not defined", name, dep))
			}
		}
	}

	if _, err := s.DeployOrder(); err != nil {
		errs = append(errs, err.Error())
	}
	return errs
}

// validateLocalPaths checks that setup sources and build contexts exist on this
// machine, so a typo fails before any droplet is created.
func (s *Spec) validateLocalPaths() Errors {
	var errs Errors
	base := filepath.Dir(s.Path)

	for _, name := range s.DropletNames() {
		setup := s.Droplets[name].Setup
		if setup == nil {
			continue
		}
		for _, file := range setup.Files {
			if file.Source == "" || file.Dest == "" {
				errs = append(errs, fmt.Sprintf("droplet %q setup: every file needs both `source` and `dest`", name))
				continue
			}
			// Dest is a path on the Linux droplet, so it is checked with the
			// slash-based rules rather than the host's. filepath.IsAbs would
			// reject "/opt/app" when doploy runs on Windows.
			if !path.IsAbs(file.Dest) {
				errs = append(errs, fmt.Sprintf("droplet %q setup: dest %q must be an absolute path on the droplet", name, file.Dest))
			}
			if _, err := os.Stat(filepath.Join(base, file.Source)); err != nil {
				errs = append(errs, fmt.Sprintf("droplet %q setup: source %q not found relative to %s", name, file.Source, base))
			}
		}
	}

	for _, name := range s.ServiceNames() {
		build := s.Services[name].Build
		if build == nil {
			continue
		}
		if build.Context == "" {
			errs = append(errs, fmt.Sprintf("service %q build: `context` is required", name))
			continue
		}

		context := filepath.Join(base, build.Context)
		info, err := os.Stat(context)
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("service %q build: context %q not found relative to %s", name, build.Context, base))
		case !info.IsDir():
			errs = append(errs, fmt.Sprintf("service %q build: context %q is not a directory", name, build.Context))
		default:
			dockerfile := build.Dockerfile
			if dockerfile == "" {
				dockerfile = "Dockerfile"
			}
			if _, err := os.Stat(filepath.Join(context, dockerfile)); err != nil {
				errs = append(errs, fmt.Sprintf("service %q build: no %s in %s", name, dockerfile, build.Context))
			}
		}
	}
	return errs
}

func (s *Spec) validateDroplets() Errors {
	var errs Errors
	seenVolumes := map[string]string{}

	for _, name := range s.DropletNames() {
		d := s.Droplets[name]

		if !namePattern.MatchString(name) {
			errs = append(errs, fmt.Sprintf("droplet %q: name must be lowercase alphanumeric with dashes", name))
		}
		if d.Region == "" {
			errs = append(errs, fmt.Sprintf("droplet %q: no `region` set and no `defaults.region` to fall back on", name))
		}
		if d.Size == "" {
			errs = append(errs, fmt.Sprintf("droplet %q: no `size` set and no `defaults.size` to fall back on", name))
		}
		if d.Image == "" {
			errs = append(errs, fmt.Sprintf("droplet %q: no `image` set and no `defaults.image` to fall back on", name))
		}

		for _, v := range d.Volumes {
			if v.SizeGB < 1 {
				errs = append(errs, fmt.Sprintf("droplet %q volume %q: `size_gb` must be at least 1", name, v.Name))
			}
			if other, dup := seenVolumes[v.Name]; dup {
				errs = append(errs, fmt.Sprintf("volume %q is declared on both %q and %q; block storage attaches to one droplet at a time", v.Name, other, name))
			}
			seenVolumes[v.Name] = name

			if fs := v.FilesystemType; fs != "" && fs != "ext4" && fs != "xfs" {
				errs = append(errs, fmt.Sprintf("droplet %q volume %q: `filesystem_type` must be ext4 or xfs", name, v.Name))
			}
		}

		if d.Firewall != nil {
			for _, rule := range d.Firewall.Inbound {
				if err := validatePortRule(rule); err != nil {
					errs = append(errs, fmt.Sprintf("droplet %q firewall: %v", name, err))
				}
			}
		}
	}

	errs = append(errs, s.validateDNSNames()...)
	return errs
}

// validateDNSNames checks the resolved DNS name of every droplet and rejects
// two droplets claiming the same name: an A record points at one address, so a
// collision means one droplet would silently win.
func (s *Spec) validateDNSNames() Errors {
	var errs Errors
	claimed := map[string]string{}

	for _, name := range s.DropletNames() {
		dns := s.Droplets[name].DNSName()
		if dns == "" {
			continue
		}
		if err := validateDNSName(dns); err != nil {
			errs = append(errs, fmt.Sprintf("droplet %q: dns %v", name, err))
			continue
		}
		if other, dup := claimed[dns]; dup {
			errs = append(errs, fmt.Sprintf("droplets %q and %q both resolve to dns name %q; set `dns:` explicitly on one, or `dns: \"\"` to opt it out of the default", other, name, dns))
		}
		claimed[dns] = name
	}
	return errs
}

// validateDNSName checks a full domain name such as "api.example.com".
func validateDNSName(name string) error {
	if len(name) > 253 {
		return fmt.Errorf("%q is longer than 253 characters", name)
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%q needs at least two labels (a bare hostname has no zone to put the record in)", name)
	}
	for _, label := range labels {
		if !dnsLabelPattern.MatchString(label) {
			return fmt.Errorf("%q is not a valid domain name (lowercase letters, digits, and dashes only)", name)
		}
	}
	return nil
}

func (s *Spec) validateServices() Errors {
	var errs Errors

	for _, name := range s.ServiceNames() {
		svc := s.Services[name]

		if !namePattern.MatchString(name) {
			errs = append(errs, fmt.Sprintf("service %q: name must be lowercase alphanumeric with dashes", name))
		}
		if svc.Image == "" && svc.Build == nil {
			errs = append(errs, fmt.Sprintf("service %q: needs either `image` (pull a prebuilt image) or `build` (build it on the droplet)", name))
		}
		if _, ok := s.Droplets[svc.Droplet]; !ok {
			errs = append(errs, fmt.Sprintf("service %q targets droplet %q, which is not defined", name, svc.Droplet))
			continue
		}

		for _, p := range svc.Ports {
			if !portPattern.MatchString(p) {
				errs = append(errs, fmt.Sprintf("service %q: %q is not a valid port mapping", name, p))
			}
		}

		// depends_on only means anything between containers sharing an engine.
		for _, dep := range svc.DependsOn {
			target, ok := s.Services[dep]
			if !ok {
				errs = append(errs, fmt.Sprintf("service %q depends on %q, which is not defined", name, dep))
				continue
			}
			if target.Droplet != svc.Droplet {
				errs = append(errs, fmt.Sprintf("service %q (on droplet %q) depends on %q (on droplet %q); depends_on only works within a single droplet", name, svc.Droplet, dep, target.Droplet))
			}
		}

		errs = append(errs, s.validateServiceVolumes(svc)...)

		if r := svc.Restart; r != "" && r != "no" && r != "always" && r != "on-failure" && r != "unless-stopped" {
			errs = append(errs, fmt.Sprintf("service %q: `restart: %s` must be no, always, on-failure, or unless-stopped", name, r))
		}
	}

	errs = append(errs, s.validateDependencyCycles()...)
	return errs
}

// validateServiceVolumes checks bind mounts that point at block storage mount
// paths actually correspond to a volume attached to the same droplet.
func (s *Spec) validateServiceVolumes(svc *Service) Errors {
	var errs Errors
	droplet := s.Droplets[svc.Droplet]

	mountPaths := map[string]bool{}
	for _, v := range droplet.Volumes {
		mountPaths[v.MountPathOrDefault()] = true
	}

	for _, mount := range svc.Volumes {
		source, _, found := strings.Cut(mount, ":")
		if !found {
			errs = append(errs, fmt.Sprintf("service %q: volume %q must be SOURCE:TARGET", svc.Name, mount))
			continue
		}
		// Only warn about absolute host paths under /mnt, which is where doploy
		// mounts block storage; anything else is the user's own host path.
		if strings.HasPrefix(source, "/mnt/") {
			matched := false
			for path := range mountPaths {
				if source == path || strings.HasPrefix(source, path+"/") {
					matched = true
					break
				}
			}
			if !matched {
				errs = append(errs, fmt.Sprintf("service %q mounts %q, but droplet %q has no block volume mounted there", svc.Name, source, svc.Droplet))
			}
		}
	}
	return errs
}

// validateDependencyCycles reports depends_on cycles, which would otherwise
// surface as an opaque compose error on the remote host.
func (s *Spec) validateDependencyCycles() Errors {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var errs Errors

	var visit func(name string, path []string)
	visit = func(name string, path []string) {
		switch state[name] {
		case done:
			return
		case inStack:
			// Trim the path back to where the cycle started.
			start := 0
			for i, p := range path {
				if p == name {
					start = i
					break
				}
			}
			errs = append(errs, fmt.Sprintf("dependency cycle: %s", strings.Join(append(path[start:], name), " -> ")))
			return
		}

		state[name] = inStack
		if svc, ok := s.Services[name]; ok {
			for _, dep := range svc.DependsOn {
				if _, ok := s.Services[dep]; ok {
					visit(dep, append(path, name))
				}
			}
		}
		state[name] = done
	}

	for _, name := range s.ServiceNames() {
		if state[name] == unvisited {
			visit(name, nil)
		}
	}
	return errs
}

// validatePortRule checks a firewall ingress entry such as "80", "53/udp", or
// "9000-9100".
func validatePortRule(rule string) error {
	portPart, proto, hasProto := strings.Cut(rule, "/")
	if hasProto && proto != "tcp" && proto != "udp" {
		return fmt.Errorf("%q: protocol must be tcp or udp", rule)
	}

	low, high, isRange := strings.Cut(portPart, "-")
	lowPort, err := strconv.Atoi(low)
	if err != nil || lowPort < 1 || lowPort > 65535 {
		return fmt.Errorf("%q: %q is not a port between 1 and 65535", rule, low)
	}
	if !isRange {
		return nil
	}

	highPort, err := strconv.Atoi(high)
	if err != nil || highPort < 1 || highPort > 65535 {
		return fmt.Errorf("%q: %q is not a port between 1 and 65535", rule, high)
	}
	if highPort < lowPort {
		return fmt.Errorf("%q: range end %d is below range start %d", rule, highPort, lowPort)
	}
	return nil
}

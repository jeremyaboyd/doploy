package spec

import (
	"fmt"
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

	if len(errs) > 0 {
		return errs
	}
	return nil
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
	return errs
}

func (s *Spec) validateServices() Errors {
	var errs Errors

	for _, name := range s.ServiceNames() {
		svc := s.Services[name]

		if !namePattern.MatchString(name) {
			errs = append(errs, fmt.Sprintf("service %q: name must be lowercase alphanumeric with dashes", name))
		}
		if svc.Image == "" {
			errs = append(errs, fmt.Sprintf("service %q: `image` is required (doploy deploys prebuilt images, it does not build them)", name))
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

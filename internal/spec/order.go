package spec

import (
	"fmt"
	"strings"
)

// DeployOrder returns droplet names in dependency order: a droplet appears
// only after everything it depends on.
//
// Ordering matters because setup is not containerised. A database installed by
// a setup block has to be accepting connections before the droplet running the
// API is touched, or the first deploy comes up with a crash-looping backend.
//
// Ties are broken alphabetically so repeated runs behave identically.
func (s *Spec) DeployOrder() ([]string, error) {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)

	state := make(map[string]int, len(s.Droplets))
	order := make([]string, 0, len(s.Droplets))

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case done:
			return nil
		case inStack:
			return fmt.Errorf("droplet dependency cycle: %s", strings.Join(append(trimToCycle(path, name), name), " -> "))
		}

		state[name] = inStack
		droplet := s.Droplets[name]
		if droplet != nil {
			for _, dep := range sortedCopy(droplet.DependsOn) {
				if _, exists := s.Droplets[dep]; !exists {
					// Reported properly by validation; skip rather than panic.
					continue
				}
				if err := visit(dep, append(path, name)); err != nil {
					return err
				}
			}
		}
		state[name] = done

		order = append(order, name)
		return nil
	}

	for _, name := range s.DropletNames() {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// trimToCycle drops the prefix of path that leads up to, but is not part of,
// the cycle, so the error names only the loop itself.
func trimToCycle(path []string, name string) []string {
	for i, p := range path {
		if p == name {
			return path[i:]
		}
	}
	return path
}

func sortedCopy(items []string) []string {
	out := append([]string{}, items...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// HasServices reports whether any service is scheduled onto a droplet. A
// droplet with only a setup block -- a database host, say -- has none, and
// skips Docker installation and compose entirely.
func (s *Spec) HasServices(droplet string) bool {
	for _, svc := range s.Services {
		if svc.Droplet == droplet {
			return true
		}
	}
	return false
}

// NeedsDocker reports whether a droplet requires a container engine.
func (s *Spec) NeedsDocker(droplet string) bool { return s.HasServices(droplet) }

// BuildServicesOn returns the services on a droplet that build from source.
func (s *Spec) BuildServicesOn(droplet string) []*Service {
	var out []*Service
	for _, svc := range s.ServicesOn(droplet) {
		if svc.Build != nil {
			out = append(out, svc)
		}
	}
	return out
}

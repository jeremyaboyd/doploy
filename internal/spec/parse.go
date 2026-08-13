package spec

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Find locates the spec file. An explicit path is returned as-is; otherwise the
// default filenames are tried in the current directory.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("reading %s: %w", explicit, err)
		}
		return explicit, nil
	}
	for _, name := range DefaultFilenames {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no %s found in the current directory (use --file to point at one)", DefaultFilenames[0])
}

// LocalPath resolves a spec-relative path against the directory holding the
// spec file, so paths in doploy.yml mean the same thing regardless of where the
// command is run from.
func (s *Spec) LocalPath(rel string) string {
	return filepath.Join(filepath.Dir(s.Path), filepath.FromSlash(rel))
}

// Load reads, interpolates, resolves, and validates a spec file.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// A .env beside the spec supplies variable values, overridable by the real
	// environment.
	dotenv, err := LoadDotEnv(filepath.Join(filepath.Dir(path), ".env"))
	if err != nil {
		return nil, err
	}
	lookup := EnvLookup(dotenv)

	// Parse into a node tree first so interpolation only touches scalar values
	// and can never corrupt the document structure.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	if err := interpolateNode(root.Content[0], lookup, false); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var s Spec
	if err := root.Content[0].Decode(&s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	s.Path = path

	if err := s.resolve(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// interpolateNode walks the YAML tree expanding variables in scalar values.
// Mapping keys are left alone, matching compose.
func interpolateNode(node *yaml.Node, lookup Lookup, isKey bool) error {
	switch node.Kind {
	case yaml.ScalarNode:
		// Single-quoted scalars are literal in compose semantics.
		if isKey || node.Style == yaml.SingleQuotedStyle || node.Tag == "!!null" {
			return nil
		}
		expanded, err := Interpolate(node.Value, lookup)
		if err != nil {
			return fmt.Errorf("line %d: %w", node.Line, err)
		}
		node.Value = expanded

	case yaml.MappingNode:
		// Content alternates key, value, key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := interpolateNode(node.Content[i], lookup, true); err != nil {
				return err
			}
			if err := interpolateNode(node.Content[i+1], lookup, false); err != nil {
				return err
			}
		}

	case yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range node.Content {
			if err := interpolateNode(child, lookup, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolve fills in names, applies defaults, synthesizes an implicit droplet,
// and assigns every service to a droplet.
func (s *Spec) resolve() error {
	if s.Droplets == nil {
		s.Droplets = map[string]*Droplet{}
	}
	if s.Services == nil {
		s.Services = map[string]*Service{}
	}

	// With no droplets block at all, everything co-locates on one implicit
	// droplet built entirely from defaults.
	if len(s.Droplets) == 0 {
		s.Droplets["default"] = &Droplet{Default: true}
	}

	for name, d := range s.Droplets {
		if d == nil {
			d = &Droplet{}
			s.Droplets[name] = d
		}
		d.Name = name
		d.applyDefaults(&s.Defaults)

		for _, v := range d.Volumes {
			if v.Name == "" {
				return fmt.Errorf("droplet %q: every volume needs a name", name)
			}
		}
	}

	fallback, err := s.fallbackDroplet()
	if err != nil {
		return err
	}

	for name, svc := range s.Services {
		if svc == nil {
			return fmt.Errorf("service %q has no definition", name)
		}
		svc.Name = name
		if svc.Droplet == "" {
			if fallback == "" {
				return fmt.Errorf("service %q must set `droplet:` (the spec defines %d droplets and none is marked `default: true`)", name, len(s.Droplets))
			}
			svc.Droplet = fallback
		}
		if svc.Restart == "" {
			svc.Restart = "unless-stopped"
		}
	}
	return nil
}

// fallbackDroplet returns the droplet services land on when they do not name
// one: the only droplet, or the one marked default. Empty means "no fallback",
// which makes `droplet:` mandatory.
func (s *Spec) fallbackDroplet() (string, error) {
	var defaults []string
	for _, name := range s.DropletNames() {
		if s.Droplets[name].Default {
			defaults = append(defaults, name)
		}
	}
	if len(defaults) > 1 {
		return "", fmt.Errorf("droplets %v are all marked `default: true`; only one may be", defaults)
	}
	if len(defaults) == 1 {
		return defaults[0], nil
	}
	if len(s.Droplets) == 1 {
		return s.DropletNames()[0], nil
	}
	return "", nil
}

// applyDefaults copies unset fields from the spec-level defaults block.
func (d *Droplet) applyDefaults(def *Defaults) {
	if d.Region == "" {
		d.Region = def.Region
	}
	if d.Size == "" {
		d.Size = def.Size
	}
	if d.Image == "" {
		d.Image = def.Image
	}
	if len(d.SSHKeys) == 0 {
		d.SSHKeys = def.SSHKeys
	}
	if d.Backups == nil {
		d.Backups = def.Backups
	}
	if d.Monitoring == nil {
		d.Monitoring = def.Monitoring
	}
	if d.IPv6 == nil {
		d.IPv6 = def.IPv6
	}
	if d.VPCUUID == "" {
		d.VPCUUID = def.VPCUUID
	}
	if d.User == "" {
		d.User = def.User
	}
	// Tags accumulate rather than override: spec-wide tags apply everywhere.
	d.Tags = append(append([]string{}, def.Tags...), d.Tags...)
}

// BoolOr dereferences an optional bool with a fallback.
func BoolOr(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

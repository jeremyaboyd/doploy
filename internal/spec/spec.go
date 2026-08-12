// Package spec defines the doploy.yml deployment specification: a
// compose-style description of DigitalOcean droplets and the containers that
// run on them.
package spec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// DefaultFilenames are searched, in order, when no --file is given.
var DefaultFilenames = []string{"doploy.yml", "doploy.yaml"}

// Spec is a parsed and resolved deployment specification.
type Spec struct {
	Version    string               `yaml:"version"`
	Project    string               `yaml:"project"`
	Defaults   Defaults             `yaml:"defaults"`
	Droplets   map[string]*Droplet  `yaml:"droplets"`
	Services   map[string]*Service  `yaml:"services"`
	Registries map[string]*Registry `yaml:"registries"`

	// Path is the file this spec was loaded from. Not part of the schema.
	Path string `yaml:"-"`
}

// Defaults supply values inherited by every droplet that does not override them.
type Defaults struct {
	Region     string   `yaml:"region"`
	Size       string   `yaml:"size"`
	Image      string   `yaml:"image"`
	SSHKeys    []string `yaml:"ssh_keys"`
	Tags       []string `yaml:"tags"`
	Backups    *bool    `yaml:"backups"`
	Monitoring *bool    `yaml:"monitoring"`
	IPv6       *bool    `yaml:"ipv6"`
	VPCUUID    string   `yaml:"vpc_uuid"`
	User       string   `yaml:"user"`
}

// Droplet is one provisioned virtual machine.
type Droplet struct {
	// Name is the map key, populated during resolution.
	Name string `yaml:"-"`

	Region  string   `yaml:"region"`
	Size    string   `yaml:"size"`
	Image   string   `yaml:"image"`
	SSHKeys []string `yaml:"ssh_keys"`
	Tags    []string `yaml:"tags"`

	Backups    *bool  `yaml:"backups"`
	Monitoring *bool  `yaml:"monitoring"`
	IPv6       *bool  `yaml:"ipv6"`
	VPCUUID    string `yaml:"vpc_uuid"`

	// User is the SSH login. Defaults to root, which is what DigitalOcean's
	// base images ship with.
	User string `yaml:"user"`

	// Default marks the droplet that services fall back to when they do not
	// name one and more than one droplet exists.
	Default bool `yaml:"default"`

	Volumes  []*Volume `yaml:"volumes"`
	Firewall *Firewall `yaml:"firewall"`

	// UserData is appended to the cloud-init script doploy generates.
	UserData string `yaml:"user_data"`
}

// Volume is a DigitalOcean block storage volume attached to a droplet.
type Volume struct {
	Name           string `yaml:"name"`
	SizeGB         int    `yaml:"size_gb"`
	FilesystemType string `yaml:"filesystem_type"`

	// MountPath defaults to /mnt/<name>. Services bind-mount from here.
	MountPath string `yaml:"mount_path"`
}

// Firewall describes a cloud firewall fronting a droplet. Outbound traffic is
// always allowed; doploy only manages ingress.
type Firewall struct {
	// Inbound entries are "80", "8080/udp", or "9000-9100".
	Inbound []string `yaml:"inbound"`

	// Sources restrict every inbound rule to these CIDRs. Defaults to
	// 0.0.0.0/0 and ::/0.
	Sources []string `yaml:"sources"`

	// SSHSources restricts port 22 specifically, which is usually the rule you
	// actually want to lock down.
	SSHSources []string `yaml:"ssh_sources"`
}

// Service is one container, expressed in a deliberately compose-like shape.
type Service struct {
	// Name is the map key, populated during resolution.
	Name string `yaml:"-"`

	// Droplet names the droplet this service runs on. Optional when the spec
	// defines exactly one droplet or marks one as default.
	Droplet string `yaml:"droplet"`

	Image       string            `yaml:"image"`
	Command     StringOrSlice     `yaml:"command"`
	Entrypoint  StringOrSlice     `yaml:"entrypoint"`
	Ports       []string          `yaml:"ports"`
	Expose      []string          `yaml:"expose"`
	Env         map[string]string `yaml:"environment"`
	EnvFile     StringOrSlice     `yaml:"env_file"`
	Volumes     []string          `yaml:"volumes"`
	DependsOn   []string          `yaml:"depends_on"`
	Restart     string            `yaml:"restart"`
	Labels      map[string]string `yaml:"labels"`
	User        string            `yaml:"user"`
	WorkingDir  string            `yaml:"working_dir"`
	Privileged  bool              `yaml:"privileged"`
	Healthcheck *Healthcheck      `yaml:"healthcheck"`
	Deploy      *Deploy           `yaml:"deploy"`
}

// Healthcheck mirrors the compose healthcheck block.
type Healthcheck struct {
	Test        StringOrSlice `yaml:"test"`
	Interval    string        `yaml:"interval"`
	Timeout     string        `yaml:"timeout"`
	Retries     int           `yaml:"retries"`
	StartPeriod string        `yaml:"start_period"`
}

// Deploy carries the subset of compose's deploy block that doploy passes
// through to the remote engine.
type Deploy struct {
	Replicas  int        `yaml:"replicas"`
	Resources *Resources `yaml:"resources"`
}

// Resources sets container CPU and memory limits.
type Resources struct {
	Limits *ResourceSpec `yaml:"limits"`
}

// ResourceSpec is a single CPU/memory pair.
type ResourceSpec struct {
	CPUs   string `yaml:"cpus"`
	Memory string `yaml:"memory"`
}

// Registry holds credentials for a private image registry.
type Registry struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// StringOrSlice accepts either a bare string or a list in YAML, matching how
// compose treats command, entrypoint, and env_file.
type StringOrSlice []string

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *StringOrSlice) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var single string
		if err := node.Decode(&single); err != nil {
			return err
		}
		*s = StringOrSlice{single}
		return nil
	case yaml.SequenceNode:
		var many []string
		if err := node.Decode(&many); err != nil {
			return err
		}
		*s = StringOrSlice(many)
		return nil
	default:
		return fmt.Errorf("line %d: expected a string or a list", node.Line)
	}
}

// MountPathOrDefault returns the host path a volume is mounted at.
func (v *Volume) MountPathOrDefault() string {
	if v.MountPath != "" {
		return v.MountPath
	}
	return "/mnt/" + v.Name
}

// UserOrDefault returns the SSH login for a droplet.
func (d *Droplet) UserOrDefault() string {
	if d.User != "" {
		return d.User
	}
	return "root"
}

// ServicesOn returns the services scheduled onto the named droplet, in a stable
// order so generated compose files do not churn between runs.
func (s *Spec) ServicesOn(droplet string) []*Service {
	var out []*Service
	for _, name := range sortedKeys(s.Services) {
		if s.Services[name].Droplet == droplet {
			out = append(out, s.Services[name])
		}
	}
	return out
}

// DropletNames returns droplet names in stable order.
func (s *Spec) DropletNames() []string { return sortedKeys(s.Droplets) }

// ServiceNames returns service names in stable order.
func (s *Spec) ServiceNames() []string { return sortedKeys(s.Services) }

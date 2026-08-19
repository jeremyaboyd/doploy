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

	// DNS is the default DNS name for droplets that do not set their own. See
	// Droplet.DNS.
	DNS string `yaml:"dns"`
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

	// DNS is the domain name pointed at this droplet's public IP: doploy
	// ensures the zone exists and upserts an A record on every deploy.
	// Unset inherits defaults.dns; an explicit empty string opts out of that
	// inheritance, which is why this is a pointer.
	DNS *string `yaml:"dns"`

	// Default marks the droplet that services fall back to when they do not
	// name one and more than one droplet exists.
	Default bool `yaml:"default"`

	Volumes  []*Volume `yaml:"volumes"`
	Firewall *Firewall `yaml:"firewall"`

	// Setup runs on the host before any containers start. It exists for
	// software that should not be containerised -- a database being the usual
	// case -- and runs whether or not the droplet has services.
	Setup *Setup `yaml:"setup"`

	// DependsOn orders deployment: every named droplet is fully set up before
	// this one is touched.
	DependsOn []string `yaml:"depends_on"`

	// UserData is appended to the cloud-init script doploy generates.
	UserData string `yaml:"user_data"`
}

// Setup describes host-level provisioning: apt packages, uploaded files, and
// shell commands, applied in that order.
//
// Commands are re-run on every deploy, so they have to be idempotent. The
// generated scripts in examples/ show the usual guard patterns.
type Setup struct {
	// Packages are installed with apt-get.
	Packages []string `yaml:"packages"`

	// Env is written to a 0600 file on the droplet and sourced before every Run
	// entry. Secrets belong here rather than inline in Run: values passed on a
	// command line show up in the droplet's process list and in doploy's own
	// output, and this keeps them out of both.
	Env map[string]string `yaml:"env"`

	// Files are uploaded before Run executes. Contents are interpolated, so a
	// config template can reference another droplet's address.
	Files []*SetupFile `yaml:"files"`

	// Run is a list of shell commands or local script paths. An entry ending in
	// .sh that exists on disk is uploaded and executed; anything else runs
	// inline.
	Run []string `yaml:"run"`
}

// SetupFile is one file copied to the droplet during setup.
type SetupFile struct {
	// Source is a path relative to the spec file.
	Source string `yaml:"source"`

	// Dest is the absolute path on the droplet.
	Dest string `yaml:"dest"`

	// Mode defaults to 0644.
	Mode string `yaml:"mode"`

	// Template enables variable interpolation of the file's contents. Off by
	// default so a SQL file full of dollar-quoted strings is left alone.
	Template bool `yaml:"template"`
}

// Build describes an image built on the droplet rather than pulled.
//
// doploy uploads the context and lets the remote engine build. That keeps an
// example runnable without a registry, at the cost of building once per host.
type Build struct {
	// Context is a directory path relative to the spec file.
	Context string `yaml:"context"`

	// Dockerfile is relative to Context. Defaults to "Dockerfile".
	Dockerfile string `yaml:"dockerfile"`

	// Args become --build-arg values.
	Args map[string]string `yaml:"args"`
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

	Image string `yaml:"image"`

	// Build, when set, makes doploy upload the context and build on the droplet
	// instead of pulling. Exactly one of Image or Build is required; with both,
	// Image names the built result.
	Build *Build `yaml:"build"`

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

// DNSName returns the DNS name pointed at this droplet, already resolved
// against defaults. Empty means no DNS is managed for it.
func (d *Droplet) DNSName() string {
	if d.DNS == nil {
		return ""
	}
	return *d.DNS
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

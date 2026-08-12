// Package deploy provisions infrastructure for a spec and then ships the
// containers onto it over SSH.
package deploy

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/provision"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/sshx"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"golang.org/x/crypto/ssh"
)

// Options configure a deployment run.
type Options struct {
	SSHKeyPath string
	SSHPort    int

	// Bootstrap installs Docker on hosts that lack it.
	Bootstrap bool

	// Wait blocks until healthchecks pass before reporting success.
	Wait bool

	// Prune removes dangling images after a successful deploy.
	Prune bool

	// InsecureHostKey disables host key verification.
	InsecureHostKey bool

	// Only limits the run to these droplet names. Empty means all.
	Only []string

	// DryRun plans without changing anything.
	DryRun bool

	// ConnectTimeout bounds how long to wait for sshd on a new droplet.
	ConnectTimeout time.Duration
}

// Deployer runs a deployment.
type Deployer struct {
	Client *godo.Client
	Spec   *spec.Spec
	Opts   Options
}

// Summary reports the outcome for one droplet.
type Summary struct {
	Droplet  string   `json:"droplet"`
	PublicIP string   `json:"public_ip"`
	Created  bool     `json:"created"`
	Services []string `json:"services"`
	Status   string   `json:"status"`
}

// Run provisions and deploys.
//
// Droplets are handled one at a time: deployments are usually one to three
// hosts, and serial output is far easier to read when something goes wrong than
// interleaved parallel logs.
func (d *Deployer) Run(ctx context.Context) ([]Summary, error) {
	targets, err := d.targetDroplets()
	if err != nil {
		return nil, err
	}

	ui.Step("Provisioning %s", plural(len(targets), "droplet"))
	p := &provision.Provisioner{Client: d.Client, Spec: d.Spec, DryRun: d.Opts.DryRun}
	result, err := p.Reconcile(ctx)
	if err != nil {
		return nil, err
	}

	if d.Opts.DryRun {
		return d.planSummaries(targets, result), nil
	}

	signer, err := sshx.LoadSigner(d.Opts.SSHKeyPath)
	if err != nil {
		return nil, err
	}
	hostKey, err := sshx.HostKeyCallback(d.Opts.InsecureHostKey)
	if err != nil {
		return nil, err
	}

	var summaries []Summary
	for _, name := range targets {
		state := result.ByName(name)
		if state == nil {
			return summaries, fmt.Errorf("droplet %q was not provisioned", name)
		}

		summary, err := d.deployDroplet(ctx, state, signer, hostKey)
		if err != nil {
			return summaries, fmt.Errorf("deploying to droplet %q: %w", name, err)
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// deployDroplet connects to one host, prepares it, and brings its services up.
func (d *Deployer) deployDroplet(ctx context.Context, state *provision.DropletState, signer ssh.Signer, hostKey ssh.HostKeyCallback) (Summary, error) {
	summary := Summary{
		Droplet:  state.Name,
		PublicIP: state.PublicIP,
		Created:  state.Created,
	}
	for _, svc := range d.Spec.ServicesOn(state.Name) {
		summary.Services = append(summary.Services, svc.Name)
	}

	if state.PublicIP == "" {
		return summary, fmt.Errorf("droplet has no public IPv4 address to connect to")
	}

	ui.Step("Deploying to %s (%s)", state.Name, state.PublicIP)

	client, err := sshx.Dial(ctx, sshx.DialOptions{
		Host:    state.PublicIP,
		Port:    d.Opts.SSHPort,
		User:    state.Spec.UserOrDefault(),
		Signer:  signer,
		HostKey: hostKey,
		MaxWait: d.Opts.ConnectTimeout,
		OnRetry: func(attempt int, err error) {
			if attempt == 1 {
				ui.Substep("waiting for sshd on %s", state.PublicIP)
			}
		},
	})
	if err != nil {
		return summary, err
	}
	defer client.Close()

	out := &logWriter{prefix: state.Name}

	if d.Opts.Bootstrap {
		ui.Substep("ensuring docker is installed")
		if err := client.Stream(dockerBootstrap, out); err != nil {
			return summary, fmt.Errorf("installing docker: %w", err)
		}
	}

	if err := d.mountVolumes(client, state, out); err != nil {
		return summary, err
	}

	if err := d.writeComposeFile(client, state.Name); err != nil {
		return summary, err
	}

	if err := d.loginRegistries(client); err != nil {
		return summary, err
	}

	ui.Substep("pulling images and starting services")
	script := composeUpScript(d.Spec.RemoteDir(), d.Spec.ComposeProjectName(), d.Opts.Wait, d.Opts.Prune)
	if err := client.Stream(script, out); err != nil {
		return summary, fmt.Errorf("bringing the stack up: %w", err)
	}

	summary.Status = d.readStatus(client)
	return summary, nil
}

// mountVolumes formats and mounts each attached block volume.
func (d *Deployer) mountVolumes(client *sshx.Client, state *provision.DropletState, out *logWriter) error {
	for _, v := range state.Spec.Volumes {
		volumeName := d.Spec.Project + "-" + v.Name
		ui.Substep("mounting volume %s at %s", volumeName, v.MountPathOrDefault())

		script := mountScript(volumeName, v.MountPathOrDefault(), v.FilesystemType)
		if err := client.Stream(script, out); err != nil {
			return fmt.Errorf("mounting volume %q: %w", v.Name, err)
		}
	}
	return nil
}

// writeComposeFile renders and uploads the compose document for a droplet.
//
// It is written 0600 because inlined environment values routinely include
// secrets.
func (d *Deployer) writeComposeFile(client *sshx.Client, droplet string) error {
	content, err := d.Spec.ComposeFile(droplet)
	if err != nil {
		return err
	}

	remote := d.remoteComposePath()
	ui.Substep("writing %s", remote)
	return client.WriteFile(remote, content, "0600")
}

// loginRegistries authenticates the droplet to each configured private
// registry so the subsequent pull can succeed.
func (d *Deployer) loginRegistries(client *sshx.Client) error {
	if len(d.Spec.Registries) == 0 {
		return nil
	}

	for _, host := range sortedRegistryHosts(d.Spec.Registries) {
		reg := d.Spec.Registries[host]
		if reg.Username == "" || reg.Password == "" {
			return fmt.Errorf("registry %q needs both `username` and `password`", host)
		}

		ui.Substep("logging in to %s as %s", host, reg.Username)
		// Not streamed: the output would echo the login command's context and
		// there is nothing useful in it beyond success or failure.
		if _, err := client.Run(registryLoginScript(host, reg)); err != nil {
			return fmt.Errorf("logging in to registry %q: %w", host, err)
		}
	}
	return nil
}

// readStatus asks compose what is running. A failure here is not fatal: the
// deploy already succeeded, and the summary line is a convenience.
func (d *Deployer) readStatus(client *sshx.Client) string {
	output, err := client.Run(composePSScript(d.Spec.RemoteDir(), d.Spec.ComposeProjectName()))
	if err != nil {
		return "unknown"
	}

	var running, total int
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		total++
		if fields := strings.Split(line, statusSeparator); len(fields) > 1 && fields[1] == "running" {
			running++
		}
	}
	if total == 0 {
		return "no containers"
	}
	return fmt.Sprintf("%d/%d running", running, total)
}

func (d *Deployer) targetDroplets() ([]string, error) {
	all := d.Spec.DropletNames()
	if len(d.Opts.Only) == 0 {
		return all, nil
	}

	valid := map[string]bool{}
	for _, name := range all {
		valid[name] = true
	}

	var selected []string
	for _, want := range d.Opts.Only {
		if !valid[want] {
			return nil, fmt.Errorf("droplet %q is not defined in %s", want, d.Spec.Path)
		}
		selected = append(selected, want)
	}
	return selected, nil
}

func (d *Deployer) planSummaries(targets []string, result *provision.Result) []Summary {
	var out []Summary
	for _, name := range targets {
		summary := Summary{Droplet: name, Status: "planned"}
		if state := result.ByName(name); state != nil {
			summary.Created = state.Created
			summary.PublicIP = state.PublicIP
		}
		for _, svc := range d.Spec.ServicesOn(name) {
			summary.Services = append(summary.Services, svc.Name)
		}
		out = append(out, summary)
	}
	return out
}

// remoteComposePath is where the generated compose file lands on the droplet.
func (d *Deployer) remoteComposePath() string {
	return path.Join(d.Spec.RemoteDir(), "docker-compose.yml")
}

func sortedRegistryHosts(registries map[string]*spec.Registry) []string {
	hosts := make([]string, 0, len(registries))
	for host := range registries {
		hosts = append(hosts, host)
	}
	for i := 1; i < len(hosts); i++ {
		for j := i; j > 0 && hosts[j] < hosts[j-1]; j-- {
			hosts[j], hosts[j-1] = hosts[j-1], hosts[j]
		}
	}
	return hosts
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// logWriter prefixes streamed remote output so it is obvious which host it came
// from when several are involved.
type logWriter struct {
	prefix string
}

func (w *logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(os.Stderr, "    %s | %s\n", w.prefix, line)
		}
	}
	return len(p), nil
}

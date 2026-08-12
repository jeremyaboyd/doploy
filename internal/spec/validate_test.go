package spec

import (
	"strings"
	"testing"
)

// expectLoadError asserts that loading a spec fails with a message containing want.
func expectLoadError(t *testing.T, content, want string) {
	t.Helper()
	_, err := Load(writeSpec(t, content))
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v\nwant it to contain %q", err, want)
	}
}

const validHeader = `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
`

func TestValidateRequiresProject(t *testing.T) {
	expectLoadError(t, `
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  api:
    image: nginx
`, "`project:` is required")
}

func TestValidateRejectsBadProjectName(t *testing.T) {
	expectLoadError(t, `
project: Demo_Project
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  api:
    image: nginx
`, "must be lowercase alphanumeric")
}

func TestValidateRequiresServices(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web: {}
`, "nothing to deploy")
}

func TestValidateRequiresImage(t *testing.T) {
	expectLoadError(t, validHeader+`
services:
  api: {}
`, "`image` is required")
}

func TestValidateRequiresRegionSizeImage(t *testing.T) {
	expectLoadError(t, `
project: demo
droplets:
  web: {}
services:
  api:
    image: nginx
`, "no `region` set")
}

func TestValidateRejectsUnknownDroplet(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web: {}
services:
  api:
    droplet: nonexistent
    image: nginx
`, "which is not defined")
}

func TestValidateRejectsCrossDropletDependsOn(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    default: true
  worker: {}
services:
  api:
    image: nginx
    depends_on: [cruncher]
  cruncher:
    droplet: worker
    image: busybox
`, "depends_on only works within a single droplet")
}

func TestValidateDetectsDependencyCycle(t *testing.T) {
	expectLoadError(t, validHeader+`
services:
  a:
    image: nginx
    depends_on: [b]
  b:
    image: nginx
    depends_on: [a]
`, "dependency cycle")
}

func TestValidateRejectsBadPortMapping(t *testing.T) {
	expectLoadError(t, validHeader+`
services:
  api:
    image: nginx
    ports: ["not-a-port"]
`, "is not a valid port mapping")
}

func TestValidateRejectsBadRestartPolicy(t *testing.T) {
	expectLoadError(t, validHeader+`
services:
  api:
    image: nginx
    restart: sometimes
`, "must be no, always, on-failure, or unless-stopped")
}

func TestValidateRejectsDuplicateVolumeAcrossDroplets(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  a:
    default: true
    volumes:
      - name: data
        size_gb: 10
  b:
    volumes:
      - name: data
        size_gb: 10
services:
  api:
    image: nginx
`, "block storage attaches to one droplet at a time")
}

func TestValidateRejectsUnbackedMountPath(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web: {}
services:
  api:
    image: nginx
    volumes:
      - /mnt/missing:/data
`, "has no block volume mounted there")
}

func TestValidateAcceptsBackedMountPath(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
droplets:
  web:
    volumes:
      - name: data
        size_gb: 20
services:
  api:
    image: nginx
    volumes:
      - /mnt/data/api:/var/lib/api
`))
	if err != nil {
		t.Fatalf("a mount under a declared volume should validate, got %v", err)
	}
	if got := s.Droplets["web"].Volumes[0].MountPathOrDefault(); got != "/mnt/data" {
		t.Errorf("mount path = %q, want /mnt/data", got)
	}
}

func TestValidateRejectsBadFirewallRule(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    firewall:
      inbound: ["99999"]
services:
  api:
    image: nginx
`, "is not a port between 1 and 65535")
}

func TestValidateRejectsInvertedPortRange(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    firewall:
      inbound: ["9000-8000"]
services:
  api:
    image: nginx
`, "below range start")
}

func TestValidateAcceptsFirewallRuleForms(t *testing.T) {
	for _, rule := range []string{"80", "443/tcp", "53/udp", "9000-9100", "9000-9100/udp"} {
		if err := validatePortRule(rule); err != nil {
			t.Errorf("validatePortRule(%q) = %v, want nil", rule, err)
		}
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Load(writeSpec(t, `
project: BAD_NAME
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  api:
    restart: maybe
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	// Both the project name and the missing image should be reported together,
	// so one run tells the user everything that is wrong.
	for _, want := range []string{"must be lowercase alphanumeric", "`image` is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

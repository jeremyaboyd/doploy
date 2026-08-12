package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSpec writes a spec file into a temp dir and returns its path.
func writeSpec(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doploy.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMinimalSpecSynthesizesADroplet(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  web:
    image: nginx:alpine
    ports: ["80:80"]
`)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Droplets) != 1 {
		t.Fatalf("expected one implicit droplet, got %d", len(s.Droplets))
	}
	d, ok := s.Droplets["default"]
	if !ok {
		t.Fatalf("expected a droplet named 'default', got %v", s.DropletNames())
	}
	if d.Region != "nyc3" || d.Size != "s-1vcpu-1gb" || d.Image != "docker-20-04" {
		t.Errorf("defaults were not applied: %+v", d)
	}
	if s.Services["web"].Droplet != "default" {
		t.Errorf("service was not assigned to the implicit droplet, got %q", s.Services["web"].Droplet)
	}
	if s.Services["web"].Restart != "unless-stopped" {
		t.Errorf("restart policy = %q, want the unless-stopped default", s.Services["web"].Restart)
	}
}

func TestLoadAssignsServicesToTheOnlyDroplet(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: ubuntu-24-04-x64
droplets:
  box: {}
services:
  api:
    image: ghcr.io/example/api:1.0
`)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Services["api"].Droplet != "box" {
		t.Errorf("service droplet = %q, want box", s.Services["api"].Droplet)
	}
}

func TestLoadRequiresExplicitDropletWhenAmbiguous(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: ubuntu-24-04-x64
droplets:
  web: {}
  worker: {}
services:
  api:
    image: ghcr.io/example/api:1.0
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error when two droplets exist and no default is marked")
	}
	if !strings.Contains(err.Error(), "must set `droplet:`") {
		t.Errorf("error should explain the fix, got: %v", err)
	}
}

func TestLoadUsesDefaultMarkedDroplet(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: ubuntu-24-04-x64
droplets:
  web:
    default: true
  worker: {}
services:
  api:
    image: ghcr.io/example/api:1.0
  cruncher:
    droplet: worker
    image: ghcr.io/example/cruncher:1.0
`)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Services["api"].Droplet; got != "web" {
		t.Errorf("api landed on %q, want the default droplet web", got)
	}
	if got := s.Services["cruncher"].Droplet; got != "worker" {
		t.Errorf("cruncher landed on %q, want worker", got)
	}
}

func TestLoadRejectsTwoDefaultDroplets(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: ubuntu-24-04-x64
droplets:
  a:
    default: true
  b:
    default: true
services:
  api:
    image: nginx
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "only one may be") {
		t.Fatalf("expected a duplicate-default error, got %v", err)
	}
}

func TestLoadInterpolatesFromEnvironment(t *testing.T) {
	t.Setenv("DOPLOY_TEST_TAG", "v2.1.0")

	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  api:
    image: ghcr.io/example/api:${DOPLOY_TEST_TAG}
    environment:
      TIER: ${DOPLOY_TEST_MISSING:-standard}
`)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Services["api"].Image; got != "ghcr.io/example/api:v2.1.0" {
		t.Errorf("image = %q, want the interpolated tag", got)
	}
	if got := s.Services["api"].Env["TIER"]; got != "standard" {
		t.Errorf("TIER = %q, want the default value", got)
	}
}

func TestLoadDoesNotInterpolateMappingKeys(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  api:
    image: nginx
    environment:
      LITERAL: 'costs $$5'
`)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Services["api"].Env["LITERAL"]; got != "costs $$5" {
		// Single-quoted scalars are literal, matching compose.
		t.Errorf("LITERAL = %q, want the single-quoted value left untouched", got)
	}
}

func TestLoadReportsRequiredVariable(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  api:
    image: nginx
    environment:
      SECRET: ${DOPLOY_TEST_REQUIRED:?set this before deploying}
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "set this before deploying") {
		t.Fatalf("expected the custom message to surface, got %v", err)
	}
}

func TestStringOrSliceAcceptsBothForms(t *testing.T) {
	path := writeSpec(t, `
project: demo
defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04
services:
  one:
    image: nginx
    command: /bin/sh -c "sleep 1"
  two:
    image: nginx
    command: ["/bin/sh", "-c", "sleep 1"]
`)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Services["one"].Command) != 1 {
		t.Errorf("scalar command = %v, want a single element", s.Services["one"].Command)
	}
	if len(s.Services["two"].Command) != 3 {
		t.Errorf("list command = %v, want three elements", s.Services["two"].Command)
	}
}

func TestFindPrefersDefaultFilename(t *testing.T) {
	if _, err := Find(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Error("expected an error for a missing explicit file")
	}
}

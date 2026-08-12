package spec

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// examplePath is the shipped example spec. Loading it here keeps the
// documentation honest: if the schema changes and the example is not updated,
// this test fails.
func examplePath() string { return filepath.Join("..", "..", "examples", "doploy.yml") }

func TestExampleSpecLoads(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@example.com:5432/app")

	s, err := Load(examplePath())
	if err != nil {
		t.Fatalf("the shipped example must stay valid: %v", err)
	}

	if s.Project != "example-app" {
		t.Errorf("project = %q", s.Project)
	}
	if len(s.Droplets) != 2 {
		t.Errorf("expected 2 droplets, got %v", s.DropletNames())
	}
	if len(s.Services) != 3 {
		t.Errorf("expected 3 services, got %v", s.ServiceNames())
	}

	// api and redis inherit the default droplet; cruncher names its own.
	for service, wantDroplet := range map[string]string{
		"api":      "web",
		"redis":    "web",
		"cruncher": "worker",
	} {
		if got := s.Services[service].Droplet; got != wantDroplet {
			t.Errorf("service %s is on %q, want %q", service, got, wantDroplet)
		}
	}

	// The worker inherits defaults it does not override.
	if got := s.Droplets["worker"].Region; got != "nyc3" {
		t.Errorf("worker region = %q, want the inherited nyc3", got)
	}
	if got := s.Droplets["web"].Size; got != "s-2vcpu-4gb" {
		t.Errorf("web size = %q, want its own override", got)
	}
}

func TestExampleSpecRequiresDatabaseURL(t *testing.T) {
	// No DATABASE_URL set: the ${...:?} form should stop the load with the
	// message written in the example.
	t.Setenv("DATABASE_URL", "")

	_, err := Load(examplePath())
	if err == nil {
		t.Fatal("expected the required-variable check to fire")
	}
	if !strings.Contains(err.Error(), "set DATABASE_URL") {
		t.Errorf("error should carry the example's own message, got: %v", err)
	}
}

func TestExampleSpecGeneratesValidComposeFiles(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@example.com:5432/app")

	s, err := Load(examplePath())
	if err != nil {
		t.Fatal(err)
	}

	for _, droplet := range s.DropletNames() {
		out, err := s.ComposeFile(droplet)
		if err != nil {
			t.Fatalf("droplet %s: %v", droplet, err)
		}

		var parsed struct {
			Services map[string]map[string]any `yaml:"services"`
			Volumes  map[string]any            `yaml:"volumes"`
		}
		if err := yaml.Unmarshal(out, &parsed); err != nil {
			t.Fatalf("droplet %s produced invalid YAML: %v\n%s", droplet, err, out)
		}
		if len(parsed.Services) == 0 {
			t.Errorf("droplet %s produced no services", droplet)
		}
	}

	// redis declares a named volume, so web's file must declare it too.
	web, err := s.ComposeFile("web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(web), "redisdata") {
		t.Error("expected the redisdata named volume to be declared in web's compose file")
	}
}

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

// microblogPath is the two-droplet example from issue #1.
func microblogPath() string {
	return filepath.Join("..", "..", "examples", "microblog", "doploy.yml")
}

// setMicroblogEnv supplies the secrets the microblog spec marks as required.
func setMicroblogEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DO_SSH_KEY", "test-key")
	t.Setenv("POSTGRES_PASSWORD", "hunter2-hunter2")
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "test-admin-password")
}

func TestMicroblogExampleLoads(t *testing.T) {
	setMicroblogEnv(t)

	s, err := Load(microblogPath())
	if err != nil {
		t.Fatalf("the microblog example must stay valid: %v", err)
	}

	if len(s.Droplets) != 2 {
		t.Fatalf("expected db and web, got %v", s.DropletNames())
	}

	// The database droplet runs no containers at all.
	if s.HasServices("db") {
		t.Error("db should have no services; postgres is installed natively")
	}
	if !s.HasServices("web") {
		t.Error("web should host the api and frontend containers")
	}

	// Both services build on the droplet, so the example needs no registry.
	for _, name := range []string{"api", "web"} {
		if s.Services[name].Build == nil {
			t.Errorf("service %q should build from source", name)
		}
	}

	setup := s.Droplets["db"].Setup
	if setup == nil {
		t.Fatal("db needs a setup block")
	}
	if len(setup.Packages) == 0 {
		t.Error("db setup should install postgres packages")
	}
	if setup.Env["APP_DB_PASSWORD"] != "hunter2-hunter2" {
		t.Errorf("setup env should be interpolated at load time, got %q", setup.Env["APP_DB_PASSWORD"])
	}
}

func TestMicroblogExampleOrdersDatabaseFirst(t *testing.T) {
	setMicroblogEnv(t)

	s, err := Load(microblogPath())
	if err != nil {
		t.Fatal(err)
	}

	order, err := s.DeployOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "db" {
		t.Errorf("db must be set up before web, got %v", order)
	}
}

func TestMicroblogExampleResolvesCrossDropletAddresses(t *testing.T) {
	setMicroblogEnv(t)

	s, err := Load(microblogPath())
	if err != nil {
		t.Fatal(err)
	}

	// Before provisioning, the addresses are still placeholders.
	refs := s.RuntimeReferences()
	if len(refs) == 0 {
		t.Fatal("the example should reference droplet addresses")
	}

	vars := RuntimeVars{}
	vars.Set("db", FieldPrivateIP, "10.116.0.2")
	vars.Set("web", FieldPrivateIP, "10.116.0.3")

	if err := s.ResolveRuntime(vars); err != nil {
		t.Fatal(err)
	}

	// The API's connection string points at the database droplet...
	dbURL := s.Services["api"].Env["DATABASE_URL"]
	if !strings.Contains(dbURL, "@10.116.0.2:5432/") {
		t.Errorf("DATABASE_URL = %q, want the db droplet's private address", dbURL)
	}
	if strings.Contains(dbURL, "${") {
		t.Errorf("DATABASE_URL still has an unresolved reference: %q", dbURL)
	}

	// ...and Postgres is told to accept exactly the web droplet.
	setup := s.Droplets["db"].Setup
	if got := setup.Env["ALLOWED_CLIENT_IP"]; got != "10.116.0.3" {
		t.Errorf("ALLOWED_CLIENT_IP = %q, want the web droplet's private address", got)
	}
	if got := setup.Env["DB_LISTEN_IP"]; got != "10.116.0.2" {
		t.Errorf("DB_LISTEN_IP = %q, want the db droplet's own address", got)
	}
}

func TestMicroblogExampleComposeOnlyCoversWeb(t *testing.T) {
	setMicroblogEnv(t)

	s, err := Load(microblogPath())
	if err != nil {
		t.Fatal(err)
	}

	dbCompose, err := s.ComposeFile("db")
	if err != nil {
		t.Fatal(err)
	}
	if dbCompose != nil {
		t.Errorf("the database droplet should get no compose file, got:\n%s", dbCompose)
	}

	webCompose, err := s.ComposeFile("web")
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Services map[string]struct {
			Build *struct {
				Context string `yaml:"context"`
			} `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(webCompose, &parsed); err != nil {
		t.Fatalf("web compose file is invalid YAML: %v\n%s", err, webCompose)
	}

	if len(parsed.Services) != 2 {
		t.Fatalf("expected api and web services, got %v", parsed.Services)
	}
	// Build contexts must point at the uploaded directories, not local paths.
	for name, svc := range parsed.Services {
		if svc.Build == nil {
			t.Errorf("service %q lost its build block", name)
			continue
		}
		want := "./build/" + name
		if svc.Build.Context != want {
			t.Errorf("service %q build context = %q, want %q", name, svc.Build.Context, want)
		}
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

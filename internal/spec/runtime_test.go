package spec

import (
	"strings"
	"testing"
)

func TestRuntimeVarsSurviveLoadTimeInterpolation(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
services:
  api:
    image: nginx
    environment:
      DB_HOST: ${droplet.db.private_ip}
droplets:
  web:
    default: true
  db: {}
`))
	if err != nil {
		t.Fatal(err)
	}

	// Load-time interpolation must leave the reference intact rather than
	// resolving it to an empty string.
	if got := s.Services["api"].Env["DB_HOST"]; got != "${droplet.db.private_ip}" {
		t.Fatalf("DB_HOST = %q, want the reference preserved for later", got)
	}
}

func TestResolveRuntimeSubstitutesAddresses(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
services:
  api:
    image: nginx
    environment:
      DATABASE_URL: postgres://app:pw@${droplet.db.private_ip}:5432/app
droplets:
  web:
    default: true
  db: {}
`))
	if err != nil {
		t.Fatal(err)
	}

	vars := RuntimeVars{}
	vars.Set("db", FieldPrivateIP, "10.116.0.5")
	vars.Set("web", FieldPrivateIP, "10.116.0.6")

	if err := s.ResolveRuntime(vars); err != nil {
		t.Fatal(err)
	}

	want := "postgres://app:pw@10.116.0.5:5432/app"
	if got := s.Services["api"].Env["DATABASE_URL"]; got != want {
		t.Errorf("DATABASE_URL = %q, want %q", got, want)
	}
}

func TestResolveRuntimeErrorsOnMissingAddress(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
services:
  api:
    image: nginx
    environment:
      DB_HOST: ${droplet.db.private_ip}
droplets:
  web:
    default: true
  db: {}
`))
	if err != nil {
		t.Fatal(err)
	}

	// db provisioned without a private address.
	vars := RuntimeVars{}
	vars.Set("db", FieldPublicIP, "203.0.113.9")

	err = s.ResolveRuntime(vars)
	if err == nil {
		t.Fatal("an unresolvable address must fail loudly, not substitute empty")
	}
	if !strings.Contains(err.Error(), "has no private_ip") {
		t.Errorf("error should say which field is missing, got: %v", err)
	}
}

func TestResolveStringLeavesShellSyntaxAlone(t *testing.T) {
	vars := RuntimeVars{}
	vars.Set("db", FieldPrivateIP, "10.0.0.4")

	// A setup script is full of shell variables and $$ that must survive.
	script := `
set -eu
HOST="${droplet.db.private_ip}"
echo "pid $$ user $USER home ${HOME}"
psql <<SQL
DO $$ BEGIN RAISE NOTICE 'hi'; END $$;
SQL
`
	got, err := vars.ResolveString(script)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, `HOST="10.0.0.4"`) {
		t.Error("the runtime reference should have been substituted")
	}
	for _, untouched := range []string{"$$", "$USER", "${HOME}", "DO $$ BEGIN"} {
		if !strings.Contains(got, untouched) {
			t.Errorf("shell/SQL syntax %q was mangled; result:\n%s", untouched, got)
		}
	}
}

func TestValidateRejectsUnknownDropletReference(t *testing.T) {
	expectLoadError(t, validHeader+`
services:
  api:
    image: nginx
    environment:
      DB_HOST: ${droplet.nonexistent.private_ip}
`, `refers to droplet "nonexistent"`)
}

func TestValidateRejectsUnknownRuntimeField(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    default: true
  db: {}
services:
  api:
    image: nginx
    environment:
      DB_HOST: ${droplet.db.ipv6_address}
`, `unknown field "ipv6_address"`)
}

func TestParseRuntimeRef(t *testing.T) {
	droplet, field, ok := ParseRuntimeRef("droplet.db.private_ip")
	if !ok || droplet != "db" || field != "private_ip" {
		t.Errorf("got (%q, %q, %v)", droplet, field, ok)
	}

	if _, _, ok := ParseRuntimeRef("POSTGRES_PASSWORD"); ok {
		t.Error("a plain variable is not a runtime reference")
	}
	if _, _, ok := ParseRuntimeRef("droplet.db"); ok {
		t.Error("a reference without a field is malformed")
	}
}

func TestDeployOrderRespectsDependencies(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
droplets:
  web:
    default: true
    depends_on: [db]
  db: {}
  cache: {}
services:
  api:
    image: nginx
`))
	if err != nil {
		t.Fatal(err)
	}

	order, err := s.DeployOrder()
	if err != nil {
		t.Fatal(err)
	}

	positions := map[string]int{}
	for i, name := range order {
		positions[name] = i
	}
	if positions["db"] > positions["web"] {
		t.Errorf("db must be deployed before web, got %v", order)
	}
	if len(order) != 3 {
		t.Errorf("every droplet should appear exactly once, got %v", order)
	}
}

func TestDeployOrderRejectsCycle(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  a:
    default: true
    depends_on: [b]
  b:
    depends_on: [a]
services:
  api:
    image: nginx
`, "droplet dependency cycle")
}

func TestValidateRejectsUnknownDropletDependency(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    default: true
    depends_on: [ghost]
services:
  api:
    image: nginx
`, `depends on "ghost"`)
}

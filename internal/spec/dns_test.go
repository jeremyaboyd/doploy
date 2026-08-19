package spec

import "testing"

func TestDropletInheritsDefaultDNS(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
  dns: example.com
services:
  web:
    image: nginx
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Droplets["default"].DNSName(); got != "example.com" {
		t.Errorf("DNSName() = %q, want the inherited default", got)
	}
}

func TestDropletOverridesDefaultDNS(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
  dns: example.com
droplets:
  web:
    default: true
    dns: api.example.com
services:
  api:
    image: nginx
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Droplets["web"].DNSName(); got != "api.example.com" {
		t.Errorf("DNSName() = %q, want the droplet's own value", got)
	}
}

func TestDropletOptsOutOfDefaultDNSWithEmptyString(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
  dns: example.com
droplets:
  web:
    default: true
    dns: example.com
  worker:
    dns: ""
services:
  api:
    image: nginx
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Droplets["worker"].DNSName(); got != "" {
		t.Errorf("DNSName() = %q, want empty after an explicit opt-out", got)
	}
}

func TestNoDNSAnywhereMeansNoDNS(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
services:
  web:
    image: nginx
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Droplets["default"].DNSName(); got != "" {
		t.Errorf("DNSName() = %q, want empty when nothing sets dns", got)
	}
}

func TestValidateRejectsDNSCollision(t *testing.T) {
	// Both droplets inherit the same default name, which would leave one of
	// them silently unreachable by that name.
	expectLoadError(t, validHeader+`
  dns: example.com
droplets:
  web:
    default: true
  worker: {}
services:
  api:
    image: nginx
`, "both resolve to dns name")
}

func TestValidateRejectsBadDNSName(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    dns: "Bad_Name.example.com"
services:
  api:
    image: nginx
`, "not a valid domain name")
}

func TestValidateRejectsSingleLabelDNSName(t *testing.T) {
	expectLoadError(t, validHeader+`
droplets:
  web:
    dns: myhost
services:
  api:
    image: nginx
`, "at least two labels")
}

func TestValidateAcceptsDNSRuntimeField(t *testing.T) {
	s, err := Load(writeSpec(t, validHeader+`
  dns: example.com
services:
  web:
    image: nginx
    environment:
      VIRTUAL_HOST: ${droplet.default.dns}
`))
	if err != nil {
		t.Fatal(err)
	}

	vars := RuntimeVars{}
	vars.Set("default", FieldDNS, s.Droplets["default"].DNSName())
	if err := s.ResolveRuntime(vars); err != nil {
		t.Fatal(err)
	}
	if got := s.Services["web"].Env["VIRTUAL_HOST"]; got != "example.com" {
		t.Errorf("VIRTUAL_HOST = %q, want the resolved dns name", got)
	}
}

func TestValidateRejectsDNSFieldOnDNSLessDroplet(t *testing.T) {
	expectLoadError(t, validHeader+`
services:
  web:
    image: nginx
    environment:
      VIRTUAL_HOST: ${droplet.default.dns}
`, "no `dns:` set")
}

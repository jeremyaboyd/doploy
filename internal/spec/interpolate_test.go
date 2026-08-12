package spec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInterpolate(t *testing.T) {
	lookup := EnvLookup(map[string]string{
		"SET":   "value",
		"EMPTY": "",
	})

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "bare name", in: "$SET", want: "value"},
		{name: "braced", in: "${SET}", want: "value"},
		{name: "embedded", in: "prefix-${SET}-suffix", want: "prefix-value-suffix"},
		{name: "unset is empty", in: "${MISSING}", want: ""},
		{name: "escaped dollar", in: "$$SET", want: "$SET"},
		{name: "literal text", in: "no variables here", want: "no variables here"},

		{name: "default when unset", in: "${MISSING:-fallback}", want: "fallback"},
		{name: "default when empty", in: "${EMPTY:-fallback}", want: "fallback"},
		{name: "no default when set", in: "${SET:-fallback}", want: "value"},

		{name: "dash default only when unset", in: "${EMPTY-fallback}", want: ""},
		{name: "dash default applies when missing", in: "${MISSING-fallback}", want: "fallback"},

		{name: "alternate when set", in: "${SET:+yes}", want: "yes"},
		{name: "alternate when empty", in: "${EMPTY:+yes}", want: ""},
		{name: "alternate when missing", in: "${MISSING:+yes}", want: ""},

		{name: "required and present", in: "${SET:?needed}", want: "value"},
		{name: "required but empty", in: "${EMPTY:?needed}", wantErr: true},
		{name: "required but missing", in: "${MISSING:?needed}", wantErr: true},

		{name: "multiple in one string", in: "${SET}/${MISSING:-x}", want: "value/x"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Interpolate(tc.in, lookup)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Interpolate(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Interpolate(%q) returned unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Interpolate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInterpolatePrefersProcessEnv(t *testing.T) {
	t.Setenv("DOPLOY_TEST_VAR", "from-env")

	lookup := EnvLookup(map[string]string{"DOPLOY_TEST_VAR": "from-dotenv"})

	got, err := Interpolate("${DOPLOY_TEST_VAR}", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Errorf("got %q, want the process environment to win over .env", got)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	content := `# a comment

TOKEN=abc123
export EXPORTED=yes
QUOTED="has spaces"
SINGLE='single quoted'
EMPTY=
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	values, err := LoadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"TOKEN":    "abc123",
		"EXPORTED": "yes",
		"QUOTED":   "has spaces",
		"SINGLE":   "single quoted",
		"EMPTY":    "",
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Errorf("%s = %q, want %q", key, values[key], expected)
		}
	}
}

func TestLoadDotEnvMissingFileIsNotAnError(t *testing.T) {
	values, err := LoadDotEnv(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("a missing .env should be tolerated, got %v", err)
	}
	if len(values) != 0 {
		t.Errorf("expected no values, got %v", values)
	}
}

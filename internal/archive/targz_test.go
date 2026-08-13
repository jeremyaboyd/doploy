package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// entriesIn lists the paths inside a tar.gz produced by TarGz.
func entriesIn(t *testing.T, data []byte) map[string]string {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	found := map[string]string{}
	reader := tar.NewReader(gz)

	for {
		header, err := reader.Next()
		if err == io.EOF {
			return found
		}
		if err != nil {
			t.Fatal(err)
		}

		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		found[header.Name] = string(body)
	}
}

// buildContext writes a directory tree and returns its path.
func buildContext(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTarGzPacksFilesWithRelativePaths(t *testing.T) {
	dir := buildContext(t, map[string]string{
		"Dockerfile":   "FROM alpine\n",
		"src/index.js": "console.log('hi');\n",
	})

	data, err := TarGz(dir)
	if err != nil {
		t.Fatal(err)
	}

	entries := entriesIn(t, data)
	if entries["Dockerfile"] != "FROM alpine\n" {
		t.Errorf("Dockerfile missing or wrong: %q", entries["Dockerfile"])
	}
	if entries["src/index.js"] != "console.log('hi');\n" {
		t.Errorf("nested file missing or wrong: %q", entries["src/index.js"])
	}

	// Paths must be relative and slash-separated so they extract correctly on
	// the droplet regardless of the machine that packed them.
	for name := range entries {
		if filepath.IsAbs(name) || len(name) > 0 && name[0] == '/' {
			t.Errorf("archive contains an absolute path: %q", name)
		}
	}
}

func TestTarGzSkipsAlwaysIgnored(t *testing.T) {
	dir := buildContext(t, map[string]string{
		"Dockerfile":              "FROM alpine\n",
		"node_modules/left-pad/i": "junk",
		".git/config":             "junk",
	})

	entries := entriesIn(t, mustTarGz(t, dir))

	for name := range entries {
		if name == "node_modules" || name == ".git" ||
			len(name) > 12 && name[:13] == "node_modules/" {
			t.Errorf("%q should have been excluded", name)
		}
	}
	if _, ok := entries[".git/config"]; ok {
		t.Error(".git contents should never be packed")
	}
}

func TestTarGzHonoursDockerignore(t *testing.T) {
	dir := buildContext(t, map[string]string{
		"Dockerfile":    "FROM alpine\n",
		".dockerignore": "secrets.env\nbuild/\n*.log\n",
		"secrets.env":   "TOKEN=nope",
		"build/out.js":  "compiled",
		"debug.log":     "noise",
		"keep.js":       "kept",
	})

	entries := entriesIn(t, mustTarGz(t, dir))

	for _, excluded := range []string{"secrets.env", "build/out.js", "debug.log"} {
		if _, present := entries[excluded]; present {
			t.Errorf("%q matched .dockerignore and should not be packed", excluded)
		}
	}
	if _, present := entries["keep.js"]; !present {
		t.Error("keep.js should have been packed")
	}
}

func TestTarGzIsDeterministic(t *testing.T) {
	dir := buildContext(t, map[string]string{
		"Dockerfile": "FROM alpine\n",
		"app.js":     "console.log(1);\n",
	})

	first := mustTarGz(t, dir)
	second := mustTarGz(t, dir)

	// Identical output keeps Docker's layer cache warm across deploys.
	if !bytes.Equal(first, second) {
		t.Error("packing the same context twice produced different bytes")
	}
}

func TestTarGzRejectsNonDirectory(t *testing.T) {
	dir := buildContext(t, map[string]string{"Dockerfile": "FROM alpine\n"})

	if _, err := TarGz(filepath.Join(dir, "Dockerfile")); err == nil {
		t.Error("a file is not a valid build context")
	}
	if _, err := TarGz(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing directory should error")
	}
}

func mustTarGz(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := TarGz(dir)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

package sshx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// useTempStore points doploy's config directory at a temp dir for one test.
func useTempStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOPLOY_CONFIG_DIR", dir)
	return dir
}

func TestGenerateStoredKeyRoundTrips(t *testing.T) {
	useTempStore(t)

	key, err := GenerateStoredKey("deploy-key")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key.PublicKey, "ssh-ed25519 ") {
		t.Errorf("public key = %q, want an ed25519 authorized_keys line", key.PublicKey)
	}
	if !strings.HasPrefix(key.Fingerprint, "SHA256:") {
		t.Errorf("fingerprint = %q, want the SHA256 form", key.Fingerprint)
	}
	if key.FingerprintMD5 == "" || strings.HasPrefix(key.FingerprintMD5, "SHA256:") {
		t.Errorf("md5 fingerprint = %q, want the legacy colon form", key.FingerprintMD5)
	}

	// The private key on disk must parse and match the recorded public half.
	data, err := os.ReadFile(key.Path)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		t.Fatalf("stored private key does not parse: %v", err)
	}
	if got := ssh.FingerprintSHA256(signer.PublicKey()); got != key.Fingerprint {
		t.Errorf("private key fingerprint %s does not match recorded %s", got, key.Fingerprint)
	}

	listed, err := ListStoredKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Name != "deploy-key" || listed[0].Fingerprint != key.Fingerprint {
		t.Errorf("ListStoredKeys() = %+v, want the generated key", listed)
	}

	signers, err := StoredSigners()
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) != 1 {
		t.Fatalf("StoredSigners() returned %d signers, want 1", len(signers))
	}
}

func TestGenerateStoredKeyRefusesOverwrite(t *testing.T) {
	useTempStore(t)

	if _, err := GenerateStoredKey("mykey"); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateStoredKey("mykey"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("regenerating an existing key: err = %v, want an already-exists refusal", err)
	}
}

func TestGenerateStoredKeyRejectsBadNames(t *testing.T) {
	useTempStore(t)

	for _, bad := range []string{"", "My_Key", "-lead", "has space", "../escape"} {
		if _, err := GenerateStoredKey(bad); err == nil {
			t.Errorf("GenerateStoredKey(%q) succeeded, want a validation error", bad)
		}
	}
}

func TestRemoveStoredKeyCleansBothHalves(t *testing.T) {
	useTempStore(t)

	key, err := GenerateStoredKey("gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveStoredKey("gone"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{key.Path, key.Path + ".pub"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists after RemoveStoredKey", path)
		}
	}
	// Removing a key that is not there is not an error.
	if err := RemoveStoredKey("gone"); err != nil {
		t.Errorf("second RemoveStoredKey errored: %v", err)
	}
}

func TestRandomKeyNameIsValidAndUnique(t *testing.T) {
	a, err := RandomKeyName()
	if err != nil {
		t.Fatal(err)
	}
	b, err := RandomKeyName()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeyName(a); err != nil {
		t.Errorf("generated name %q fails validation: %v", a, err)
	}
	if a == b {
		t.Errorf("two generated names collided: %q", a)
	}
}

func TestTrustedHostsParsesKnownHosts(t *testing.T) {
	dir := useTempStore(t)

	key, err := GenerateStoredKey("host-source")
	if err != nil {
		t.Fatal(err)
	}
	// Any public key works as a host key for parsing purposes.
	pubLine := strings.Fields(key.PublicKey)
	line := "203.0.113.7 " + pubLine[0] + " " + pubLine[1] + "\n"
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := TrustedHosts()
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 {
		t.Fatalf("TrustedHosts() returned %d entries, want 1", len(hosts))
	}
	if hosts[0].Hosts[0] != "203.0.113.7" || hosts[0].Type != "ssh-ed25519" || hosts[0].Fingerprint != key.Fingerprint {
		t.Errorf("entry = %+v, want the written host", hosts[0])
	}
}

func TestTrustedHostsMissingFileIsEmpty(t *testing.T) {
	useTempStore(t)

	hosts, err := TrustedHosts()
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 0 {
		t.Errorf("TrustedHosts() = %+v, want empty with no file", hosts)
	}
}

func TestLoadSignersFindsStoredKeys(t *testing.T) {
	useTempStore(t)

	if _, err := GenerateStoredKey("auto"); err != nil {
		t.Fatal(err)
	}
	signers, err := LoadSigners("")
	if err != nil {
		t.Fatal(err)
	}
	if len(signers) == 0 {
		t.Error("LoadSigners(\"\") found no signers despite a stored key")
	}
}

package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jeremyaboyd/doploy/internal/config"
	"golang.org/x/crypto/ssh"
)

// The key store holds private keys doploy generated, one file per key named
// after the key (plus a .pub sibling). Keys here are found automatically at
// deploy time, so a key made with `doploy add ssh` needs no --ssh-key flag.

// keyNamePattern matches spec.namePattern so a stored key's name can always be
// referenced from a spec's ssh_keys list.
var keyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// StoredKey describes one key in doploy's local key store.
type StoredKey struct {
	Name string `json:"name"`
	Path string `json:"path"`

	// PublicKey is the authorized_keys form uploaded to DigitalOcean.
	PublicKey string `json:"public_key"`

	// Fingerprint is the SHA256 form modern OpenSSH prints.
	Fingerprint string `json:"fingerprint"`

	// FingerprintMD5 is the legacy colon-separated form the DigitalOcean API
	// reports, kept so the two listings can be matched up.
	FingerprintMD5 string `json:"fingerprint_md5"`
}

// KeysDir returns the key store directory, creating it if needed.
func KeysDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", keys, err)
	}
	return keys, nil
}

// ValidateKeyName rejects names that could not be referenced from a spec or
// would misbehave as filenames.
func ValidateKeyName(name string) error {
	if !keyNamePattern.MatchString(name) {
		return fmt.Errorf("key name %q must be lowercase alphanumeric with dashes", name)
	}
	return nil
}

// RandomKeyName returns a unique fallback name for a key created without one.
func RandomKeyName() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating key name: %w", err)
	}
	return "doploy-" + hex.EncodeToString(b[:]), nil
}

// GenerateStoredKey creates an ed25519 keypair and writes it into the store.
// It refuses to overwrite: a private key that already exists may already be
// authorized on droplets, and regenerating it would lock doploy out of them.
func GenerateStoredKey(name string) (*StoredKey, error) {
	if err := ValidateKeyName(name); err != nil {
		return nil, err
	}
	dir, err := KeysDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("key %q already exists at %s", name, path)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, name)
	if err != nil {
		return nil, fmt.Errorf("encoding private key: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("encoding public key: %w", err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + name + "\n"
	if err := os.WriteFile(path+".pub", []byte(authorized), 0o644); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("writing %s.pub: %w", path, err)
	}

	return describeKey(name, path, sshPub, authorized), nil
}

// RemoveStoredKey deletes a key from the store. It exists so a failed upload
// can undo the generation it belonged to.
func RemoveStoredKey(name string) error {
	dir, err := KeysDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(path + ".pub"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ListStoredKeys returns every key in the store, sorted by name.
func ListStoredKeys() ([]StoredKey, error) {
	dir, err := KeysDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var keys []StoredKey
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, ".pub") {
			continue
		}
		path := filepath.Join(dir, name)

		data, err := os.ReadFile(path + ".pub")
		if err != nil {
			// A private key with no .pub sibling is half a key; report it
			// rather than hiding it, but with nothing to fingerprint.
			keys = append(keys, StoredKey{Name: name, Path: path})
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			keys = append(keys, StoredKey{Name: name, Path: path})
			continue
		}
		keys = append(keys, *describeKey(name, path, pub, string(data)))
	}
	// ReadDir already sorts by filename.
	return keys, nil
}

// StoredSigners loads every usable private key in the store, for use as
// authentication candidates when dialing a droplet. Unparseable files are
// skipped: one corrupt key should not block deploys that use another.
func StoredSigners() ([]ssh.Signer, error) {
	keys, err := ListStoredKeys()
	if err != nil {
		return nil, err
	}
	var signers []ssh.Signer
	for _, k := range keys {
		data, err := os.ReadFile(k.Path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			continue
		}
		signers = append(signers, signer)
	}
	return signers, nil
}

func describeKey(name, path string, pub ssh.PublicKey, authorized string) *StoredKey {
	return &StoredKey{
		Name:           name,
		Path:           path,
		PublicKey:      strings.TrimSpace(authorized),
		Fingerprint:    ssh.FingerprintSHA256(pub),
		FingerprintMD5: ssh.FingerprintLegacyMD5(pub),
	}
}

// TrustedHost is one entry in doploy's known_hosts file: a host key recorded
// on first use.
type TrustedHost struct {
	Hosts       []string `json:"hosts"`
	Type        string   `json:"type"`
	Fingerprint string   `json:"fingerprint"`
}

// TrustedHosts parses doploy's known_hosts file. A missing file means no
// droplet has been connected to yet, which is an empty list rather than an
// error.
func TrustedHosts() ([]TrustedHost, error) {
	path, err := KnownHostsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var hosts []TrustedHost
	for len(data) > 0 {
		_, entries, pub, _, rest, err := ssh.ParseKnownHosts(data)
		if err != nil {
			// io.EOF ends the file; anything else is a malformed line, which
			// the verifier would also reject -- point at the file.
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		hosts = append(hosts, TrustedHost{
			Hosts:       entries,
			Type:        pub.Type(),
			Fingerprint: ssh.FingerprintSHA256(pub),
		})
		data = rest
	}
	return hosts, nil
}

// Package sshx wraps golang.org/x/crypto/ssh with the pieces doploy needs:
// key discovery, trust-on-first-use host keys, and file upload over an exec
// channel.
package sshx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremyaboyd/doploy/internal/ui"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// defaultKeyNames are searched under ~/.ssh when no key is given explicitly,
// newest key type first.
var defaultKeyNames = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// LoadSigners collects the private keys to offer a droplet, in order.
//
// An explicit path is that key and nothing else. Otherwise every key in
// doploy's own store is offered first (they exist precisely to be found
// automatically), then the first standard key under ~/.ssh. SSH tries the
// candidates in order until one is accepted, so extra keys cost nothing.
func LoadSigners(path string) ([]ssh.Signer, error) {
	if path != "" {
		signer, err := loadSignerFile(path)
		if err != nil {
			return nil, err
		}
		return []ssh.Signer{signer}, nil
	}

	signers, err := StoredSigners()
	if err != nil {
		return nil, err
	}

	if found, err := discoverKey(); err == nil {
		signer, err := loadSignerFile(found)
		switch {
		case err == nil:
			signers = append(signers, signer)
		case len(signers) > 0:
			// A store key can already authenticate, so a home key that needs a
			// passphrase (or fails to parse) should not block the deploy.
			ui.Warn("skipping %s: %v", found, err)
		default:
			return nil, err
		}
	}

	if len(signers) == 0 {
		home, _ := os.UserHomeDir()
		keysDir, _ := KeysDir()
		return nil, fmt.Errorf("no SSH private key found in %s (looked for %s) and none in doploy's key store (%s); run `doploy add ssh` to create one, or pass --ssh-key",
			filepath.Join(home, ".ssh"), strings.Join(defaultKeyNames, ", "), keysDir)
	}
	return signers, nil
}

// loadSignerFile loads one private key, prompting for a passphrase if the key
// is encrypted.
func loadSignerFile(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, fmt.Errorf("reading SSH key %s: %w", path, err)
	}

	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer, nil
	}

	var needsPassphrase *ssh.PassphraseMissingError
	if !errors.As(err, &needsPassphrase) {
		return nil, fmt.Errorf("parsing SSH key %s: %w", path, err)
	}

	passphrase, err := promptPassphrase(path)
	if err != nil {
		return nil, err
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(data, passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypting SSH key %s: %w", path, err)
	}
	return signer, nil
}

// discoverKey finds the first standard private key in ~/.ssh.
func discoverKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	for _, name := range defaultKeyNames {
		candidate := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no SSH private key found in %s (looked for %s); pass --ssh-key",
		filepath.Join(home, ".ssh"), strings.Join(defaultKeyNames, ", "))
}

// promptPassphrase reads a passphrase from the terminal without echoing it.
func promptPassphrase(path string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("SSH key %s is passphrase-protected and stdin is not a terminal; use an unencrypted key or an agent-free key for automation", path)
	}
	fmt.Fprintf(os.Stderr, "Passphrase for %s: ", path)
	pass, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("reading passphrase: %w", err)
	}
	return pass, nil
}

// expandHome resolves a leading ~ in a path.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), string(filepath.Separator)))
}

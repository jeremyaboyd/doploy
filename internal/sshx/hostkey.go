package sshx

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/jeremyaboyd/doploy/internal/config"
	"github.com/jeremyaboyd/doploy/internal/ui"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsPath is doploy's own known_hosts file. It is deliberately separate
// from ~/.ssh/known_hosts so doploy never rewrites the user's file.
func KnownHostsPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "known_hosts"), nil
}

// HostKeyCallback returns a trust-on-first-use callback.
//
// An unknown host is recorded and accepted, which is the only workable policy
// for droplets that did not exist a minute ago. A host key that *changed*,
// however, is rejected: that is the case where the warning actually means
// something.
func HostKeyCallback(insecure bool) (ssh.HostKeyCallback, error) {
	if insecure {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	path, err := KnownHostsPath()
	if err != nil {
		return nil, err
	}
	// knownhosts.New requires the file to exist.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return nil, fmt.Errorf("creating %s: %w", path, err)
		}
	}

	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}

		// Want is populated only when we have keys on file for this host and
		// none of them matched: a genuine mismatch, not a first sighting.
		if len(keyErr.Want) > 0 {
			return fmt.Errorf("host key for %s changed (%s); if you rebuilt this droplet, remove its line from %s",
				hostname, ssh.FingerprintSHA256(key), path)
		}

		ui.Substep("trusting new host key for %s (%s)", hostname, ssh.FingerprintSHA256(key))
		return appendKnownHost(path, hostname, remote, key)
	}, nil
}

// appendKnownHost records a newly seen host key.
func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	addresses := []string{knownhosts.Normalize(hostname)}
	if remote != nil {
		if normalized := knownhosts.Normalize(remote.String()); normalized != addresses[0] {
			addresses = append(addresses, normalized)
		}
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	if _, err := fmt.Fprintln(file, knownhosts.Line(addresses, key)); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

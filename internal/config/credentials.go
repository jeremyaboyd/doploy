// Package config persists doploy's credentials and local state.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Method identifies how a token was obtained. OAuth is not implemented yet, but
// the field exists so stored credentials survive the transition.
type Method string

const (
	MethodPAT   Method = "pat"
	MethodOAuth Method = "oauth"
)

// EnvToken and EnvTokenAlt mirror the environment variables doctl honours, so
// CI can supply a token without running `doploy auth init`.
const (
	EnvToken    = "DIGITALOCEAN_ACCESS_TOKEN"
	EnvTokenAlt = "DO_TOKEN"
)

// ErrNotAuthenticated is returned when no token is available from any source.
var ErrNotAuthenticated = errors.New("not authenticated: run `doploy auth init` or set " + EnvToken)

// Credentials is the on-disk credential record.
type Credentials struct {
	Method Method `json:"method"`
	Token  string `json:"token"`

	// OAuth-only fields, unused by the PAT flow.
	RefreshToken string     `json:"refresh_token,omitempty"`
	Expiry       *time.Time `json:"expiry,omitempty"`

	// Account is a cache of who the token belongs to, so `auth status` can show
	// something useful even when offline.
	AccountEmail string `json:"account_email,omitempty"`
	AccountUUID  string `json:"account_uuid,omitempty"`

	// FromEnv is set when the token came from the environment rather than disk.
	FromEnv bool `json:"-"`
}

// Expired reports whether an OAuth token needs refreshing. PATs never expire.
func (c *Credentials) Expired() bool {
	if c.Expiry == nil {
		return false
	}
	return time.Now().After(*c.Expiry)
}

// dirPath resolves the configuration directory without touching the disk.
//
// Path() is called while building help text, so merely running `doploy --help`
// must not create directories as a side effect.
func dirPath() (string, error) {
	if override := os.Getenv("DOPLOY_CONFIG_DIR"); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(base, "doploy"), nil
}

// Dir returns doploy's configuration directory, creating it if needed.
func Dir() (string, error) {
	dir, err := dirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

// credentialsPath resolves the credential file without creating anything.
func credentialsPath() (string, error) {
	dir, err := dirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// Load returns credentials from the environment if present, otherwise from
// disk. It returns ErrNotAuthenticated when neither source has a token.
func Load() (*Credentials, error) {
	for _, key := range []string{EnvToken, EnvTokenAlt} {
		if tok := os.Getenv(key); tok != "" {
			return &Credentials{Method: MethodPAT, Token: tok, FromEnv: true}, nil
		}
	}

	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotAuthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if creds.Token == "" {
		return nil, ErrNotAuthenticated
	}
	return &creds, nil
}

// Save writes credentials to disk with owner-only permissions.
func Save(creds *Credentials) error {
	// Saving is the one operation that needs the directory to exist.
	if _, err := Dir(); err != nil {
		return err
	}
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}

	// Write to a temp file in the same directory, then rename, so an
	// interrupted write cannot leave a truncated credential file behind.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("saving credentials: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// Delete removes stored credentials. It is not an error if none exist.
func Delete() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Path exposes the credential file location for `auth status`.
func Path() string {
	p, err := credentialsPath()
	if err != nil {
		return "(unknown)"
	}
	return p
}

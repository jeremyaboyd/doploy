package sshx

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client is a connection to one droplet.
type Client struct {
	conn *ssh.Client
	host string
}

// DialOptions configures a connection attempt.
type DialOptions struct {
	Host    string
	Port    int
	User    string
	Signer  ssh.Signer
	HostKey ssh.HostKeyCallback
	Timeout time.Duration // per-attempt dial timeout
	MaxWait time.Duration // total time to keep retrying
	OnRetry func(attempt int, err error)
}

// Dial connects to a droplet, retrying until sshd is accepting connections.
//
// A freshly created droplet reports "active" through the API before its SSH
// daemon is listening, so the first several attempts failing is normal rather
// than an error worth surfacing.
func Dial(ctx context.Context, opts DialOptions) (*Client, error) {
	if opts.Port == 0 {
		opts.Port = 22
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.MaxWait == 0 {
		opts.MaxWait = 5 * time.Minute
	}

	cfg := &ssh.ClientConfig{
		User:            opts.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(opts.Signer)},
		HostKeyCallback: opts.HostKey,
		Timeout:         opts.Timeout,
	}

	addr := net.JoinHostPort(opts.Host, fmt.Sprint(opts.Port))
	deadline := time.Now().Add(opts.MaxWait)
	var lastErr error

	for attempt := 1; ; attempt++ {
		conn, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			return &Client{conn: conn, host: opts.Host}, nil
		}
		lastErr = err

		// An authentication failure will never succeed on retry, so fail fast
		// rather than burning the whole retry budget on it.
		if strings.Contains(err.Error(), "unable to authenticate") ||
			strings.Contains(err.Error(), "host key") {
			return nil, fmt.Errorf("connecting to %s@%s: %w", opts.User, addr, err)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connecting to %s@%s after %s: %w", opts.User, addr, opts.MaxWait, lastErr)
		}
		if opts.OnRetry != nil {
			opts.OnRetry(attempt, err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// Close releases the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Host returns the address this client is connected to.
func (c *Client) Host() string { return c.host }

// Run executes a command and returns its combined output. A non-zero exit
// status becomes an error carrying that output, since the remote stderr is
// almost always the useful part of the diagnosis.
func (c *Client) Run(cmd string) (string, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("opening session: %w", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(out), fmt.Errorf("remote command failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Stream executes a command, copying its output to w as it arrives. Use this
// for long operations such as image pulls, where waiting in silence is worse
// than the noise.
func (c *Client) Stream(cmd string, w io.Writer) error {
	session, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("opening session: %w", err)
	}
	defer session.Close()

	session.Stdout = w
	session.Stderr = w
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("remote command failed: %w", err)
	}
	return nil
}

// WriteFile uploads content to a remote path with the given mode.
//
// The payload is base64-encoded and decoded on the far side, which keeps
// arbitrary bytes safe through a shell without needing a second SFTP channel.
// The write goes to a temporary file and is renamed into place so a failure
// never leaves a half-written compose file behind.
func (c *Client) WriteFile(remotePath string, content []byte, mode string) error {
	if mode == "" {
		mode = "0644"
	}
	encoded := base64.StdEncoding.EncodeToString(content)
	dir := path.Dir(remotePath)
	tmp := remotePath + ".doploy.tmp"

	script := strings.Join([]string{
		"set -eu",
		"mkdir -p " + shellQuote(dir),
		fmt.Sprintf("printf '%%s' %s | base64 -d > %s", shellQuote(encoded), shellQuote(tmp)),
		fmt.Sprintf("chmod %s %s", mode, shellQuote(tmp)),
		fmt.Sprintf("mv %s %s", shellQuote(tmp), shellQuote(remotePath)),
	}, "\n")

	if _, err := c.Run(script); err != nil {
		return fmt.Errorf("writing %s: %w", remotePath, err)
	}
	return nil
}

// shellQuote wraps a string in single quotes, escaping any it contains.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

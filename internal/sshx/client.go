package sshx

import (
	"bytes"
	"context"
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
	Signers []ssh.Signer
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
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(opts.Signers...)},
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

// runWithStdin executes a command with r streamed to its stdin, returning the
// combined output on failure the same way Run does.
func (c *Client) runWithStdin(cmd string, r io.Reader) (string, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("opening session: %w", err)
	}
	defer session.Close()

	session.Stdin = r
	out, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(out), fmt.Errorf("remote command failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// WriteFile uploads content to a remote path with the given mode.
//
// The payload is streamed over the session's stdin rather than embedded in the
// command line: a single shell argument is capped at MAX_ARG_STRLEN (128 KiB
// on Linux), which a base64-embedded payload hits at ~96 KiB of content.
// The write goes to a temporary file and is renamed into place so a failure
// never leaves a half-written compose file behind.
func (c *Client) WriteFile(remotePath string, content []byte, mode string) error {
	if mode == "" {
		mode = "0644"
	}
	dir := path.Dir(remotePath)
	tmp := remotePath + ".doploy.tmp"

	script := strings.Join([]string{
		"set -eu",
		"mkdir -p " + shellQuote(dir),
		"cat > " + shellQuote(tmp),
		fmt.Sprintf("chmod %s %s", mode, shellQuote(tmp)),
		fmt.Sprintf("mv %s %s", shellQuote(tmp), shellQuote(remotePath)),
	}, "\n")

	if _, err := c.runWithStdin(script, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("writing %s: %w", remotePath, err)
	}
	return nil
}

// WriteArchive uploads a gzipped tar and extracts it into remoteDir.
//
// The archive is streamed over the session's stdin (see WriteFile for why the
// command line cannot carry it). The directory is replaced rather than merged:
// a file deleted locally must not survive on the droplet and end up in the
// next image build.
func (c *Client) WriteArchive(remoteDir string, tarGz []byte) error {
	staging := remoteDir + ".doploy.new"

	script := strings.Join([]string{
		"set -eu",
		"rm -rf " + shellQuote(staging),
		"mkdir -p " + shellQuote(staging),
		"tar xzf - -C " + shellQuote(staging),
		"rm -rf " + shellQuote(remoteDir),
		"mkdir -p " + shellQuote(path.Dir(remoteDir)),
		fmt.Sprintf("mv %s %s", shellQuote(staging), shellQuote(remoteDir)),
	}, "\n")

	if _, err := c.runWithStdin(script, bytes.NewReader(tarGz)); err != nil {
		return fmt.Errorf("uploading archive to %s: %w", remoteDir, err)
	}
	return nil
}

// shellQuote wraps a string in single quotes, escaping any it contains.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

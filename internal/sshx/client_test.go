package sshx

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// execRecord is one command the fake server received: the exec string and
// everything the client streamed to its stdin.
type execRecord struct {
	cmd   string
	stdin []byte
}

// startTestServer runs a minimal in-process SSH server that accepts every
// session, records each exec request and its stdin, and reports success.
func startTestServer(t *testing.T, records chan<- execRecord) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}

	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(nc, cfg, records)
		}
	}()

	return ln.Addr().String()
}

func serveConn(nc net.Conn, cfg *ssh.ServerConfig, records chan<- execRecord) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range chReqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				cmdLen := binary.BigEndian.Uint32(req.Payload)
				cmd := string(req.Payload[4 : 4+cmdLen])
				req.Reply(true, nil)

				stdin, _ := io.ReadAll(ch)
				records <- execRecord{cmd: cmd, stdin: stdin}

				ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, 0))
				return
			}
		}()
	}
}

func dialTestServer(t *testing.T, addr string) *Client {
	t.Helper()
	conn, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "root",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("dialing test server: %v", err)
	}
	client := &Client{conn: conn, host: addr}
	t.Cleanup(func() { client.Close() })
	return client
}

// A payload comfortably past Linux's MAX_ARG_STRLEN (131,072 bytes): embedding
// it in the command line — as the old base64-argument upload did — would make
// the remote shell refuse to execute at all.
func largePayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	return payload
}

func TestWriteArchiveStreamsLargePayloadOverStdin(t *testing.T) {
	records := make(chan execRecord, 1)
	client := dialTestServer(t, startTestServer(t, records))

	payload := largePayload(256 * 1024)
	if err := client.WriteArchive("/opt/doploy/app/build/web", payload); err != nil {
		t.Fatalf("WriteArchive: %v", err)
	}

	rec := <-records
	if len(rec.cmd) > 4096 {
		t.Errorf("command is %d bytes; the payload must not be embedded in the command line", len(rec.cmd))
	}
	if !strings.Contains(rec.cmd, "tar xzf -") {
		t.Errorf("command does not extract from stdin:\n%s", rec.cmd)
	}
	if !bytes.Equal(rec.stdin, payload) {
		t.Errorf("stdin carried %d bytes, want %d matching bytes", len(rec.stdin), len(payload))
	}
}

func TestWriteFileStreamsLargePayloadOverStdin(t *testing.T) {
	records := make(chan execRecord, 1)
	client := dialTestServer(t, startTestServer(t, records))

	payload := largePayload(192 * 1024)
	if err := client.WriteFile("/opt/doploy/app/compose.yml", payload, "0600"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := <-records
	if len(rec.cmd) > 4096 {
		t.Errorf("command is %d bytes; the payload must not be embedded in the command line", len(rec.cmd))
	}
	if !bytes.Equal(rec.stdin, payload) {
		t.Errorf("stdin carried %d bytes, want %d matching bytes", len(rec.stdin), len(payload))
	}
}

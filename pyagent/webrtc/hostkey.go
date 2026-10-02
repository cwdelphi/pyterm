package webrtc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Host key policies
const (
	HostKeyTOFU     = "tofu"     // Trust On First Use: unknown host saved, mismatch rejected
	HostKeyStrict   = "strict"   // Must already exist in known_hosts
	HostKeyInsecure = "insecure" // Legacy: ignore host key (not recommended)
)

var (
	hostKeyOnce     sync.Once
	hostKeyPolicy   = HostKeyTOFU
	knownHostsPath  = "known_hosts"
	hostKeyCallback gossh.HostKeyCallback
	hostKeyInitErr  error
)

// InitHostKeyConfig configures host key verification once at startup.
// policy: tofu | strict | insecure; knownHosts is path to known_hosts file.
func InitHostKeyConfig(policy, knownHosts string) {
	hostKeyOnce.Do(func() {
		if policy == "" {
			policy = HostKeyTOFU
		}
		switch policy {
		case HostKeyTOFU, HostKeyStrict, HostKeyInsecure:
			hostKeyPolicy = policy
		default:
			hostKeyPolicy = HostKeyTOFU
		}
		if knownHosts != "" {
			knownHostsPath = knownHosts
		}
		if hostKeyPolicy == HostKeyInsecure {
			hostKeyCallback = gossh.InsecureIgnoreHostKey()
			return
		}
		if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o755); err == nil || filepath.Dir(knownHostsPath) == "." {
			// ensure file exists so knownhosts.New does not fail on missing file for strict/empty
			if _, err := os.Stat(knownHostsPath); errors.Is(err, os.ErrNotExist) {
				_ = os.WriteFile(knownHostsPath, nil, 0o600)
			}
		}
		base, err := knownhosts.New(knownHostsPath)
		if err != nil {
			hostKeyInitErr = fmt.Errorf("known_hosts init: %w", err)
			hostKeyCallback = gossh.InsecureIgnoreHostKey()
			return
		}
		hostKeyCallback = func(hostname string, remote net.Addr, key gossh.PublicKey) error {
			err := base(hostname, remote, key)
			if err == nil {
				return nil
			}
			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) {
				return err
			}
			// Unknown host (no want entries): TOFU accepts and records; strict rejects.
			if len(keyErr.Want) == 0 {
				if hostKeyPolicy != HostKeyTOFU {
					return fmt.Errorf("host key unknown for %s (strict): %w", hostname, err)
				}
				return appendKnownHost(hostname, key)
			}
			// Known host with mismatched key → always reject (MITM).
			return fmt.Errorf("HOST KEY VERIFICATION FAILED for %s: remote key does not match known_hosts: %w", hostname, err)
		}
	})
}

// HostKeyCallback returns the configured callback (defaults to TOFU if not initialized).
func HostKeyCallback() gossh.HostKeyCallback {
	InitHostKeyConfig("", "")
	return hostKeyCallback
}

// HostKeyPolicy returns current policy string.
func HostKeyPolicy() string { return hostKeyPolicy }

// HostKeyInitError returns deferred init error if any.
func HostKeyInitError() error { return hostKeyInitErr }

// appendKnownHost writes a new known_hosts line (TOFU).
func appendKnownHost(hostname string, key gossh.PublicKey) error {
	line := knownhosts.Line([]string{hostname}, key)
	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("append known_hosts: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("append known_hosts: %w", err)
	}
	return nil
}

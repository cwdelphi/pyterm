package webrtc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectDeployMode(t *testing.T) {
	root := t.TempDir()
	if got := detectDeployMode(root); got != "host" {
		t.Fatalf("empty root should be host, got %s", got)
	}
	if err := os.WriteFile(filepath.Join(root, ".dockerenv"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectDeployMode(root); got != "docker" {
		t.Fatalf("with .dockerenv should be docker, got %s", got)
	}
}

func TestPersistBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload.bin")
	dst := filepath.Join(dir, "bin")
	if err := os.WriteFile(src, []byte("ELF-ish-new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := persistBinary(src, dst); err != nil {
		t.Fatalf("persist failed: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ELF-ish-new-binary" {
		t.Fatalf("dst content mismatch: %s", b)
	}
}

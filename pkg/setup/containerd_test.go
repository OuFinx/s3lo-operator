package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHostsConfig(t *testing.T) {
	tmpDir := t.TempDir()

	err := WriteHostsConfig(tmpDir, "5732")
	if err != nil {
		t.Fatalf("WriteHostsConfig failed: %v", err)
	}

	hostsFile := filepath.Join(tmpDir, "s3.local", "hosts.toml")
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		t.Fatalf("hosts.toml not created: %v", err)
	}

	content := string(data)

	if !strings.Contains(content, "http://localhost:5732") {
		t.Errorf("hosts.toml missing localhost URL, got:\n%s", content)
	}

	if !strings.Contains(content, `capabilities = ["pull", "resolve"]`) {
		t.Errorf("hosts.toml missing capabilities, got:\n%s", content)
	}
}

func TestWriteHostsConfig_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	certsDir := filepath.Join(tmpDir, "certs.d")

	err := WriteHostsConfig(certsDir, "5732")
	if err != nil {
		t.Fatalf("WriteHostsConfig failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(certsDir, "s3.local", "hosts.toml")); os.IsNotExist(err) {
		t.Fatal("hosts.toml not created in new directory")
	}
}

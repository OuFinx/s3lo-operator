package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

const hostsTemplate = `server = "http://localhost:%s"

[host."http://localhost:%s"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
`

// WriteHostsConfig writes the containerd hosts.toml for the "s3" registry host.
func WriteHostsConfig(certsDir, port string) error {
	dir := filepath.Join(certsDir, "s3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create hosts dir %s: %w", dir, err)
	}

	content := fmt.Sprintf(hostsTemplate, port, port)
	hostsFile := filepath.Join(dir, "hosts.toml")

	if err := os.WriteFile(hostsFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write hosts.toml: %w", err)
	}

	return nil
}

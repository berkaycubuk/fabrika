package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Save serializes c back to <repoRoot>/fabrika.toml. It validates c first and
// refuses to write an invalid manifest. The write is atomic: it marshals to a
// temp file in the same directory and renames it into place, so a reader never
// observes a partially written manifest. The result round-trips through Load.
func Save(repoRoot string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}

	data, err := toml.Marshal(*c)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", FileName, err)
	}

	path := filepath.Join(repoRoot, FileName)
	tmp, err := os.CreateTemp(repoRoot, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", FileName, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", FileName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", FileName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", FileName, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}

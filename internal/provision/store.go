package provision

import (
	"encoding/json"
	"os"
)

// ConfigPath is where the saved installation lives.
func ConfigPath(installRoot string) string {
	return winJoin(installRoot, "config.json")
}

// Save writes the configuration, including the OIDC client secret and the
// Gitea admin token.
//
// The 0600 mode is advisory on Windows, where ACLs are what actually matter.
// The installer places this under ProgramData and restricts the directory to
// Administrators and SYSTEM; this file must never move somewhere world-readable.
func Save(c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(c.InstallRoot), b, 0o600)
}

// Load reads a saved configuration. A missing file is reported as
// ErrNotProvisioned so callers can show a wizard instead of an error.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, ErrNotProvisioned
		}
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	// Tolerate hand-edited files that dropped a port.
	if c.RdpgwPort == 0 {
		c.RdpgwPort = DefaultRdpgwPort
	}
	if c.GiteaPort == 0 {
		c.GiteaPort = DefaultGiteaPort
	}
	return c, nil
}

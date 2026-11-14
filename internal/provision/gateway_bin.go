package provision

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// EnsureGatewayBinary makes sure rdpgw.exe exists at dst. The installer ships
// the gateway beside the app, so on a normal install this is one copy. It
// does nothing when the file is already there, so it is safe to call on every
// refresh of the wizard.
func EnsureGatewayBinary(dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	src := filepath.Join(filepath.Dir(self), "rdpgw.exe")
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("the gateway program is missing from the installation (%s); reinstall the app", src)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

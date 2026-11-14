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

// EnsureGatewayTemplates copies the gateway's web pages and logos into the
// gateway folder: templates\ for the pages, assets\ for the logos. rdpgw reads
// them from its working directory, and without them the pages fall back to a
// bare version with no server list and broken images. These are copied every
// time, so an upgrade or a Repair brings in new files.
func EnsureGatewayTemplates(dstDir string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	base := filepath.Dir(self)
	for _, pair := range [][2]string{
		{"rdpgw-templates", "templates"},
		{"rdpgw-assets", "assets"},
	} {
		src := filepath.Join(base, pair[0])
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("the gateway's %s are missing from the installation (%s); reinstall the app", pair[1], src)
		}
		if err := copyTree(src, filepath.Join(dstDir, pair[1])); err != nil {
			return err
		}
	}
	return nil
}

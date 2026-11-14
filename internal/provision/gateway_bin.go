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

// EnsureGatewayTemplates copies the gateway's web pages into dstDir/templates.
// rdpgw reads them from its working directory and falls back to a bare page
// without them, which has no working server list. Unlike the binary, these are
// copied every time, so an upgrade or a Repair brings in new pages.
func EnsureGatewayTemplates(dstDir string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	src := filepath.Join(filepath.Dir(self), "rdpgw-templates")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("the gateway's web pages are missing from the installation (%s); reinstall the app", src)
	}
	dst := filepath.Join(dstDir, "templates")
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

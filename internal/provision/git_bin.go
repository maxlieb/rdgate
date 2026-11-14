package provision

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureGitBinary makes sure a Git is available under dstDir, and returns the
// path of git.exe. Gitea will not open its database without one, and most
// Windows machines do not have Git installed.
//
// The installer ships MinGit (the portable Git for Windows build, GPL-2.0)
// beside the app in a "git" folder, so this is a copy on a normal install and
// does nothing when the copy is already there.
func EnsureGitBinary(dstDir string) (string, error) {
	exe := filepath.Join(dstDir, "cmd", "git.exe")
	if _, err := os.Stat(exe); err == nil {
		return exe, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	src := filepath.Join(filepath.Dir(self), "git")
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("Git is missing from the installation (%s); reinstall the app", src)
	}
	if err := copyTree(src, dstDir); err != nil {
		return "", fmt.Errorf("copying Git: %w", err)
	}
	return exe, nil
}

// copyTree copies every file under src into dst, keeping the folder layout.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
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

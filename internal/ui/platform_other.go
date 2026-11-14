//go:build !windows

package ui

import (
	"errors"
	"os"
)

// The product targets Windows. These stubs exist so the package builds and
// its tests run on a development machine, rather than forcing every check
// behind a build tag.

func openURL(string) error {
	return errors.New("opening links is only supported on Windows")
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

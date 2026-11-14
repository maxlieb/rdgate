//go:build windows

package ui

import (
	"context"
	"os"
	"syscall"

	"github.com/maxlieb/rdgw-manager/internal/provision"
)

// openURL hands a URL or ms-settings: link to the shell.
//
// rundll32 is used rather than "cmd /c start" because start treats the first
// quoted argument as a window title, which silently does nothing for URLs
// containing characters that need quoting.
func openURL(url string) error {
	cmd := provision.Command(context.Background(), "rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

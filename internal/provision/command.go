package provision

import (
	"context"
	"os/exec"
	"time"
)

// Command starts a helper program without a console window.
//
// The desktop app is a GUI program, so Windows gives each console program it
// starts a new console window unless told otherwise. Without this, every
// tailscale, netsh and schtasks call flashes a CMD window in front of the
// person. Use this instead of exec.Command everywhere in the app.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	// When the context ends, the process is killed. But if it left a child
	// holding the output pipe open, Wait would block forever. WaitDelay makes
	// Go stop waiting for the pipes a few seconds after the kill.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

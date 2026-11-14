//go:build !windows

package provision

import "os/exec"

func hideConsole(*exec.Cmd) {}

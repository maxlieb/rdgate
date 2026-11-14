package provision

import (
	"context"
	"os"
)

// UninstallOptions controls how much is removed. The distinction matters:
// most people uninstalling want the services gone but might reinstall later,
// and silently destroying their account and settings is a poor surprise.
type UninstallOptions struct {
	// KeepData leaves the install directory in place, including Gitea's
	// database with the user account and TOTP enrollment.
	KeepData bool

	// ResetFunnel withdraws what we published. Left on by default: leaving a
	// machine publicly reachable after an uninstall would be indefensible.
	ResetFunnel bool
}

// DefaultUninstall removes the services and unpublishes, keeping data.
func DefaultUninstall() UninstallOptions {
	return UninstallOptions{KeepData: true, ResetFunnel: true}
}

// Uninstall tears the installation down.
//
// Every step is attempted even if an earlier one failed, and the errors are
// collected rather than returned at the first problem. A teardown that stops
// halfway is worse than one that reports three partial failures, because the
// machine is left published with no services behind it.
func Uninstall(ctx context.Context, c Config, opts UninstallOptions, report Reporter) []error {
	var errs []error

	report.step("Stopping services", StepRunning, "")
	for _, name := range []string{TaskGateway, TaskSignIn, TaskCaddy, TaskDDNS} {
		StopTask(ctx, name)
	}
	for _, name := range []string{TaskGateway, TaskSignIn, TaskCaddy, TaskDDNS} {
		if err := RemoveTask(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	report.step("Stopping services", StepDone, "scheduled tasks removed")

	// Close what install opened. The router mapping is removed on a best
	// effort basis: if the router is gone or does not answer, the lease
	// expires by itself.
	RemoveFirewallRules(ctx)
	if c.FrontDoor == FrontDoorDirect {
		if igd, err := DiscoverIGD(ctx); err == nil {
			_ = igd.DeleteMapping(ctx, PublicPort)
		}
	}

	if opts.ResetFunnel && (c.FrontDoor == FrontDoorFunnel || c.FrontDoor == "") {
		report.step("Unpublishing", StepRunning, "withdrawing the public address")
		if ts, err := FindTailscale(); err == nil {
			if err := ts.ResetFunnel(ctx); err != nil {
				errs = append(errs, err)
				report.step("Unpublishing", StepFailed, err.Error())
			} else {
				report.step("Unpublishing", StepDone, "no longer reachable from the internet")
			}
		} else {
			// Tailscale already gone: nothing is published either.
			report.step("Unpublishing", StepSkipped, "Tailscale not installed")
		}
	}

	if opts.KeepData {
		report.step("Removing files", StepSkipped, "keeping your settings and sign-in account")
		return errs
	}

	report.step("Removing files", StepRunning, c.InstallRoot)
	if c.InstallRoot == "" {
		errs = append(errs, errEmptyInstallRoot)
		report.step("Removing files", StepFailed, "install path unknown")
		return errs
	}
	if err := os.RemoveAll(c.InstallRoot); err != nil {
		errs = append(errs, err)
		report.step("Removing files", StepFailed, err.Error())
	} else {
		report.step("Removing files", StepDone, "deleted")
	}
	return errs
}

// errEmptyInstallRoot guards against a config that lost its path turning
// os.RemoveAll("") into something unpredictable.
var errEmptyInstallRoot = &pathError{"refusing to delete: the install path is empty"}

type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }

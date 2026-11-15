package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/maxlieb/rdgw-manager/internal/provision"
	"github.com/maxlieb/rdgw-manager/internal/update"
)

// signingCertFile is the public certificate the installer places beside RDGate.
const signingCertFile = "rdgate-signing.cer"

// CheckUpdate asks GitHub whether a newer release exists, and whether this
// install can verify and install it.
func (a *App) CheckUpdate() (update.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), update.Timeout)
	defer cancel()
	rel, err := update.Latest(ctx, update.NewClient(), update.LatestAPI())
	if err != nil {
		return update.Status{}, err
	}
	st := update.Status{Current: update.Version, Latest: rel.Version, Newer: update.Newer(rel.Version, update.Version)}
	if st.Newer {
		if _, err := update.InstalledThumbprint(filepath.Join(appDir(), signingCertFile)); err != nil {
			st.Reason = err.Error()
		} else {
			st.CanInstall = true
		}
	}
	return st, nil
}

// InstallUpdate downloads the newest release, checks that it is signed by the
// certificate this install trusts, and then hands the install to a script that
// runs after RDGate has closed. The gateway is down for the install and comes
// back afterwards, with the tasks that were running before.
func (a *App) InstallUpdate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := update.NewClient()

	rel, err := update.Latest(ctx, client, update.LatestAPI())
	if err != nil {
		return err
	}
	if !update.Newer(rel.Version, update.Version) {
		return errors.New("this is already the latest version")
	}
	dir := filepath.Join(os.Getenv("ProgramData"), "RDGateway", "updates")
	want, err := update.InstalledThumbprint(filepath.Join(appDir(), signingCertFile))
	if err != nil {
		return err
	}
	installer, err := update.Download(ctx, client, rel.SetupURL, dir)
	if err != nil {
		return err
	}
	if err := update.VerifySignature(ctx, installer, want); err != nil {
		os.Remove(installer)
		return err
	}

	tasks := []string{provision.TaskGateway, provision.TaskSignIn, provision.TaskCaddy, provision.TaskGate, provision.TaskDDNS}
	script := update.InstallScript(os.Getpid(), installer, appDir(), filepath.Join(dir, "update.log"), tasks)
	scriptPath := filepath.Join(dir, "install-update.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return err
	}
	cmd := provision.Command(context.Background(), "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", scriptPath)
	if err := cmd.Start(); err != nil {
		return err
	}
	if a.Quit != nil {
		a.Quit()
	}
	return nil
}

func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

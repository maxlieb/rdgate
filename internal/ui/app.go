// Package ui exposes the provisioning core to the Wails frontend.
//
// Every method here is deliberately thin: it names something the person can
// see on screen, and delegates. Keeping the Wails import out of this file
// means the whole backend surface compiles and can be exercised without the
// GUI toolkit present.
package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maxlieb/rdgw-manager/internal/provision"
)

// App is bound to the frontend. Method names match the strings in index.html.
type App struct {
	mu   sync.RWMutex
	cfg  provision.Config
	path string // where config.json lives

	// lastStatus lets the window paint immediately on open instead of showing
	// an empty frame while the first round of checks runs.
	lastStatus *provision.Status

	// Emit pushes an event to the frontend. main.go wires this to Wails's
	// runtime.EventsEmit; keeping it a plain function means this package has
	// no dependency on the GUI toolkit and still compiles and tests alone.
	Emit func(event string, data any)
}

func (a *App) emit(event string, data any) {
	a.mu.RLock()
	f := a.Emit
	a.mu.RUnlock()
	if f != nil {
		f(event, data)
	}
}

func New(configPath string) *App {
	a := &App{path: configPath}
	if cfg, err := provision.Load(configPath); err == nil {
		a.cfg = cfg
	}
	return a
}

// Configured reports whether an installation exists, which is how the
// frontend decides between the dashboard and the first-run wizard.
func (a *App) Configured() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.PublicHost() != ""
}

// Status runs every check and returns the dashboard.
func (a *App) Status() provision.Status {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st := provision.Gather(ctx, cfg)

	// Tailscale may have learned the hostname since the config was written,
	// for example after a first sign-in. Persist it so the next launch has it.
	if cfg.Hostname == "" && st.Hostname != "" {
		a.mu.Lock()
		a.cfg.Hostname = st.Hostname
		cfg = a.cfg
		a.mu.Unlock()
		_ = provision.Save(cfg)
	}

	a.mu.Lock()
	a.lastStatus = &st
	a.mu.Unlock()
	return st
}

// --- First run ------------------------------------------------------------

// WizardState is what the setup screen needs to decide which step to show.
// It is a snapshot of reality rather than a stored position, so closing the
// app half way through and reopening it resumes in the right place instead
// of starting over or claiming a step was done.
type WizardState struct {
	TailscaleInstalled bool   `json:"tailscaleInstalled"`
	SignedIn           bool   `json:"signedIn"`
	Hostname           string `json:"hostname"`
	RdpgwPresent       bool   `json:"rdpgwPresent"`
	RdpgwExpectedAt    string `json:"rdpgwExpectedAt"`
	ThisMachine        string `json:"thisMachine"`
	AlreadyInstalled   bool   `json:"alreadyInstalled"`
}

func (a *App) Wizard() WizardState {
	a.mu.RLock()
	root := a.cfg.InstallRoot
	installed := a.cfg.PublicHost() != "" && a.cfg.ClientID != ""
	a.mu.RUnlock()
	if root == "" {
		root = defaultInstallRoot()
	}

	st := WizardState{
		ThisMachine:      hostnameOrBlank(),
		AlreadyInstalled: installed,
		RdpgwExpectedAt:  root + `\rdpgw\rdpgw.exe`,
	}
	_ = provision.EnsureGatewayBinary(st.RdpgwExpectedAt)
	if _, err := os.Stat(st.RdpgwExpectedAt); err == nil {
		st.RdpgwPresent = true
	}

	ts, err := provision.FindTailscale()
	if err != nil {
		return st
	}
	st.TailscaleInstalled = true

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if s, err := ts.State(ctx); err == nil {
		st.SignedIn = s.LoggedIn && s.Running
		st.Hostname = s.Hostname
	}
	return st
}

// InstallTailscale shells out to winget. Returning promptly matters more than
// reporting completion: winget shows its own progress, and the wizard polls
// Wizard() to notice when the binary appears.
func (a *App) InstallTailscale() error {
	cmd := provision.Command(context.Background(), "winget", "install", "--id", "Tailscale.Tailscale",
		"--silent", "--accept-package-agreements", "--accept-source-agreements")
	return cmd.Start()
}

// InstallOptions is what the wizard collects. Door decides which of the other
// fields matter; the rest are ignored rather than rejected, so the frontend can
// send one shape regardless of which card was chosen.
type InstallOptions struct {
	Door      string   `json:"door"` // "funnel" | "direct" | "relay"
	Targets   []string `json:"targets"`
	Domain    string   `json:"domain"`
	Email     string   `json:"email"`
	DuckName  string   `json:"duckName"`
	DuckToken string   `json:"duckToken"`
	DDNSURL   string   `json:"ddnsUrl"`
}

// CheckDirect answers "can this connection work without a server?" before the
// person commits to the direct option.
func (a *App) CheckDirect() provision.DirectCheck {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return provision.CheckDirect(ctx)
}

// RunInstall provisions everything, emitting "install:step" for each step so
// the wizard can draw a live checklist rather than a spinner.
func (a *App) RunInstall(opts InstallOptions) (*provision.Result, error) {
	if len(opts.Targets) == 0 {
		return nil, errors.New("choose at least one computer to make reachable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	a.mu.RLock()
	root := a.cfg.InstallRoot
	a.mu.RUnlock()
	if root == "" {
		root = defaultInstallRoot()
	}

	cfg, err := provision.NewConfig(root)
	if err != nil {
		return nil, err
	}
	cfg.Targets = opts.Targets
	cfg.ACMEEmail = opts.Email

	switch opts.Door {
	case "direct":
		cfg.FrontDoor = provision.FrontDoorDirect
		cfg.PublicDomain = opts.Domain
		switch {
		case opts.DuckName != "":
			if cfg.DDNSURL, err = provision.DuckDNSURL(opts.DuckName, opts.DuckToken); err != nil {
				return nil, err
			}
			// A DuckDNS name is the address, so the person need not type it twice.
			if cfg.PublicDomain == "" {
				cfg.PublicDomain = strings.TrimSuffix(strings.ToLower(opts.DuckName), ".duckdns.org") + ".duckdns.org"
			}
		case opts.DDNSURL != "":
			cfg.DDNSURL = opts.DDNSURL
		}

	case "relay", "funnel", "":
		ts, err := provision.FindTailscale()
		if err != nil {
			return nil, errors.New("Tailscale is not installed yet")
		}
		state, err := ts.WaitForBackend(ctx, 2*time.Minute)
		if err != nil {
			return nil, errors.New("Tailscale is not signed in yet")
		}
		cfg.Hostname = state.Hostname
		if opts.Door == "relay" {
			cfg.FrontDoor = provision.FrontDoorRelay
			cfg.PublicDomain = opts.Domain
			if cfg.TailscaleIP, err = ts.IPv4(ctx); err != nil {
				return nil, err
			}
		}

	default:
		return nil, errors.New("unknown way of reaching the gateway: " + opts.Door)
	}

	res, err := provision.Install(ctx, cfg, func(e provision.Event) {
		a.emit("install:step", e)
	})
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.cfg = res.Config
	a.mu.Unlock()
	return res, nil
}

// RelayScript returns the script for the relay server, regenerated from the
// saved settings so it can be copied again after the wizard is gone.
func (a *App) RelayScript() (string, error) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if cfg.FrontDoor != provision.FrontDoorRelay {
		return "", errors.New("this gateway does not use a relay server")
	}
	return provision.VPSScript(cfg)
}

// TestPublic runs the end-to-end check on demand: a real HTTPS request to the
// public name, which exercises DNS, the server and the tunnel together.
func (a *App) TestPublic() provision.Check {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return provision.CheckPublic(ctx, cfg)
}

func defaultInstallRoot() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = "."
	}
	return filepath.Join(root, "RDGateway")
}

func hostnameOrBlank() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// --- Remedies -------------------------------------------------------------
// Each corresponds to one button that appears beside a failing check. They
// return an error rather than reporting success optimistically, because the
// button turns back into a usable control on failure.

// OpenPortal opens the gateway's own portal in the default browser.
func (a *App) OpenPortal() error {
	a.mu.RLock()
	url := a.cfg.PortalURL()
	host := a.cfg.PublicHost()
	a.mu.RUnlock()
	if host == "" {
		return errors.New("no address yet")
	}
	return openURL(url)
}

// OpenTwoFactor sends the user to Gitea's enrollment page. There is no API
// for enrolling TOTP, so this is a handoff rather than something the app can
// do on the user's behalf.
func (a *App) OpenTwoFactor() error {
	a.mu.RLock()
	url := a.cfg.TwoFactorURL()
	host := a.cfg.PublicHost()
	a.mu.RUnlock()
	if host == "" {
		return errors.New("no address yet")
	}
	return openURL(url)
}

// OpenRDPSettings jumps straight to the Remote Desktop page in Settings
// rather than describing where to find it.
func (a *App) OpenRDPSettings() error {
	return openURL("ms-settings:remotedesktop")
}

// TailscaleLogin starts the sign-in flow; Tailscale opens its own browser tab.
func (a *App) TailscaleLogin() error {
	ts, err := provision.FindTailscale()
	if err != nil {
		return errors.New("Tailscale is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := provision.Command(ctx, ts.Exe, "up")
	return cmd.Start() // non-blocking: the user finishes in the browser
}

// EnableFunnel republishes both ports.
func (a *App) EnableFunnel() error {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()

	ts, err := provision.FindTailscale()
	if err != nil {
		return errors.New("Tailscale is not installed")
	}
	if cfg.FrontDoor == provision.FrontDoorDirect || cfg.FrontDoor == provision.FrontDoorRelay {
		return errors.New("this gateway uses your own domain, so there is nothing to publish here")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return ts.EnableFunnel(ctx, cfg.CaddyPort)
}

func (a *App) RestartGateway() error { return restart(provision.TaskGateway) }
func (a *App) RestartSignIn() error  { return restart(provision.TaskSignIn) }

func restart(task string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	provision.StopTask(ctx, task)
	return provision.StartTask(ctx, task)
}

// --- Settings -------------------------------------------------------------

// Settings is the editable subset of the configuration. Keys and secrets are
// deliberately absent: there is no reason to show them, and every reason not
// to put them on a screen someone might be sharing.
type Settings struct {
	Door      string   `json:"door"`
	Hostname  string   `json:"hostname"`
	Targets   []string `json:"targets"`
	AdminUser string   `json:"adminUser"`
	Portal    string   `json:"portal"`
	SignIn    string   `json:"signIn"`
}

func (a *App) GetSettings() Settings {
	a.mu.RLock()
	defer a.mu.RUnlock()
	door := string(a.cfg.FrontDoor)
	if door == "" {
		door = "funnel"
	}
	return Settings{
		Door:      door,
		Hostname:  a.cfg.PublicHost(),
		Targets:   a.cfg.Targets,
		AdminUser: a.cfg.AdminUser,
		Portal:    a.cfg.PortalURL(),
		SignIn:    a.cfg.Issuer(),
	}
}

// SetTargets rewrites the allowlist and restarts the gateway so the change
// takes effect, because a setting that needs a manual restart to apply is a
// setting people will think is broken.
func (a *App) SetTargets(targets []string) error {
	if len(targets) == 0 {
		return errors.New("keep at least one computer in the list, or nothing can be reached")
	}
	a.mu.Lock()
	a.cfg.Targets = targets
	cfg := a.cfg
	a.mu.Unlock()

	yaml, err := provision.RdpgwYAML(cfg)
	if err != nil {
		return err
	}
	if err := writeFile(cfg.InstallRoot+`\rdpgw\rdpgw.yaml`, yaml); err != nil {
		return err
	}
	if err := provision.Save(cfg); err != nil {
		return err
	}
	return a.RestartGateway()
}

// Uninstall tears everything down. purge also deletes the account and its
// two-factor enrollment, so the frontend asks before passing true.
func (a *App) Uninstall(purge bool) []string {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	errs := provision.Uninstall(ctx, cfg,
		provision.UninstallOptions{KeepData: !purge, ResetFunnel: true}, nil)

	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

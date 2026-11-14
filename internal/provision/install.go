package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// StepState describes where a provisioning step got to. The UI renders these
// as a running checklist, which is why "skipped" is distinct from "done" -
// re-running the installer should visibly do less, not pretend to redo work.
type StepState string

const (
	StepRunning StepState = "running"
	StepDone    StepState = "done"
	StepFailed  StepState = "failed"
	StepSkipped StepState = "skipped"
)

// Event is one update from a long-running operation.
type Event struct {
	Step    string    `json:"step"`
	State   StepState `json:"state"`
	Message string    `json:"message"`
}

// Reporter receives progress. The Wails UI passes a function that forwards to
// the frontend; the CLI passes one that prints.
type Reporter func(Event)

func (r Reporter) step(name string, state StepState, msg string) {
	if r == nil {
		return
	}
	r(Event{Step: name, State: state, Message: msg})
}

// Result is what the caller shows the user once installation finishes.
type Result struct {
	Config Config `json:"config"`

	// AdminPassword is returned, never persisted. It is shown once; if the
	// user loses it they reset it through the Gitea CLI rather than finding
	// it sitting in a file.
	AdminPassword string `json:"adminPassword"`

	// RelayScript is what to run on the server, in relay mode only.
	RelayScript string `json:"relayScript,omitempty"`

	// TwoFactorURL is where the wizard sends them next. Enrollment is
	// enforced, so this is not optional housekeeping - it is the last step
	// of installation.
	TwoFactorURL string `json:"twoFactorUrl"`
}

// Install provisions everything and returns what the user needs to finish.
//
// The order matters and is not arbitrary: Gitea has to be running before its
// OAuth application can be created, and rdpgw's config cannot be rendered
// until that application exists and yields a client id and secret.
func Install(ctx context.Context, c Config, report Reporter) (*Result, error) {
	switch c.FrontDoor {
	case FrontDoorDirect, FrontDoorRelay:
		host, err := NormalizeHost(c.PublicDomain)
		if err != nil {
			return nil, err
		}
		c.PublicDomain = host
		if err := validateEmail(c.ACMEEmail); err != nil {
			return nil, err
		}
		if c.FrontDoor == FrontDoorRelay {
			// Relay needs the tailnet address so the home Caddy can bind to
			// it alone, and so the server's script knows where to send.
			if c.TailscaleIP == "" {
				ts, err := FindTailscale()
				if err != nil {
					return nil, errors.New("relay mode needs Tailscale installed and signed in")
				}
				ip, err := ts.IPv4(ctx)
				if err != nil {
					return nil, err
				}
				c.TailscaleIP = ip
			}
		}
	default:
		c.FrontDoor = FrontDoorFunnel
		if c.Hostname == "" {
			return nil, errors.New("no hostname: sign in to Tailscale first so the machine has a public name")
		}
	}
	if c.CaddyPort == 0 {
		c.CaddyPort = DefaultCaddyPort
	}
	if len(c.Targets) == 0 {
		return nil, errors.New("no targets selected: the gateway would refuse every connection")
	}
	if err := ValidateKeys(c); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", c.InstallRoot, err)
	}

	// --- Sign-in service -----------------------------------------------
	gitea := NewGitea(c)
	if err := gitea.EnsureBinary(ctx, c.GiteaVersion, report); err != nil {
		report.step("Sign-in service", StepFailed, err.Error())
		return nil, err
	}

	report.step("Sign-in configuration", StepRunning, "")
	if err := gitea.WriteConfig(c); err != nil {
		report.step("Sign-in configuration", StepFailed, err.Error())
		return nil, err
	}
	report.step("Sign-in configuration", StepDone, "two-factor enrollment enforced")

	password, err := NewPassword(20)
	if err != nil {
		return nil, err
	}
	report.step("Account", StepRunning, "creating "+c.AdminUser)
	if _, err := EnsureGitBinary(winJoin(c.InstallRoot, "git")); err != nil {
		report.step("Sign-in service", StepFailed, err.Error())
		return nil, err
	}
	if err := gitea.Migrate(ctx); err != nil {
		report.step("Account", StepFailed, err.Error())
		return nil, err
	}
	if err := gitea.CreateAdmin(ctx, c.AdminUser, password); err != nil {
		report.step("Account", StepFailed, err.Error())
		return nil, err
	}
	report.step("Account", StepDone, c.AdminUser)

	report.step("Starting sign-in service", StepRunning, "")
	if err := RegisterTask(ctx, TaskSignIn, "RD Gateway sign-in service",
		gitea.Exe, `web --config "`+gitea.AppIni+`"`, gitea.WorkPath); err != nil {
		report.step("Starting sign-in service", StepFailed, err.Error())
		return nil, err
	}
	if err := StartTask(ctx, TaskSignIn); err != nil {
		report.step("Starting sign-in service", StepFailed, err.Error())
		return nil, err
	}
	if err := gitea.WaitUntilListening(ctx, 90*time.Second); err != nil {
		report.step("Starting sign-in service", StepFailed, err.Error())
		return nil, err
	}
	report.step("Starting sign-in service", StepDone, "listening")

	// --- OIDC client ----------------------------------------------------
	report.step("Connecting gateway to sign-in", StepRunning, "")
	gitea.RemoveSetupTokens(ctx, c.AdminUser, password)
	token, err := gitea.CreateAdminToken(ctx, c.AdminUser)
	if err != nil {
		report.step("Connecting gateway to sign-in", StepFailed, err.Error())
		return nil, err
	}
	c.GiteaAdminToken = token

	// Registering the OAuth application needs write:user. A separate token is
	// minted for that one call, so the status token stays read-only.
	writeToken, err := gitea.MintToken(ctx, c.AdminUser, "write:user")
	if err != nil {
		report.step("Connecting gateway to sign-in", StepFailed, err.Error())
		return nil, err
	}
	clientID, clientSecret, err := gitea.CreateOAuthApp(ctx, writeToken, c.RedirectURI())
	if err != nil {
		report.step("Connecting gateway to sign-in", StepFailed, err.Error())
		return nil, err
	}
	c.ClientID, c.ClientSecret = clientID, clientSecret
	report.step("Connecting gateway to sign-in", StepDone, "OAuth application registered")

	// --- Gateway ---------------------------------------------------------
	report.step("Gateway configuration", StepRunning, "")
	rdpgwDir := winJoin(c.InstallRoot, "rdpgw")
	if err := os.MkdirAll(rdpgwDir, 0o700); err != nil {
		return nil, err
	}
	yaml, err := RdpgwYAML(c)
	if err != nil {
		report.step("Gateway configuration", StepFailed, err.Error())
		return nil, err
	}
	if err := os.WriteFile(winJoin(rdpgwDir, "rdpgw.yaml"), []byte(yaml), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(winJoin(rdpgwDir, "default.rdp"), []byte(DefaultRDP()), 0o644); err != nil {
		return nil, err
	}
	report.step("Gateway configuration", StepDone, "")

	rdpgwExe := winJoin(rdpgwDir, "rdpgw.exe")
	if err := EnsureGatewayBinary(rdpgwExe); err != nil {
		report.step("Starting gateway", StepFailed, err.Error())
		return nil, err
	}

	report.step("Starting gateway", StepRunning, "")
	if err := RegisterGateway(ctx, c.InstallRoot); err != nil {
		report.step("Starting gateway", StepFailed, err.Error())
		return nil, err
	}
	if err := StartTask(ctx, TaskGateway); err != nil {
		report.step("Starting gateway", StepFailed, err.Error())
		return nil, err
	}
	report.step("Starting gateway", StepDone, "")

	// --- Web front end ----------------------------------------------------
	// Everything public goes through Caddy on one port, in both modes.
	caddy := NewCaddy(c)
	if err := caddy.EnsureBinary(ctx, c.CaddyVersion, report); err != nil {
		report.step("Web front end", StepFailed, err.Error())
		return nil, err
	}
	if err := caddy.WriteConfig(c); err != nil {
		report.step("Web front end", StepFailed, err.Error())
		return nil, err
	}
	if err := RegisterTask(ctx, TaskCaddy, "RD Gateway web front end",
		caddy.Exe, `run --config "`+caddy.Caddyfile+`" --adapter caddyfile`, caddy.Dir); err != nil {
		report.step("Web front end", StepFailed, err.Error())
		return nil, err
	}
	if err := StartTask(ctx, TaskCaddy); err != nil {
		report.step("Web front end", StepFailed, err.Error())
		return nil, err
	}
	report.step("Web front end", StepDone, "listening on "+itoa(c.CaddyListenPort()))

	// --- Publish ---------------------------------------------------------
	var relayScript string
	switch c.FrontDoor {
	case FrontDoorDirect:
		report.step("Publishing", StepRunning, "opening this PC's firewall for the gateway")
		if err := AllowDirect(ctx, caddy.Exe); err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		ctl, err := ctlPath()
		if err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		if err := Save(c); err != nil { // the updater reads the saved config
			return nil, fmt.Errorf("saving configuration: %w", err)
		}
		if err := RegisterTask(ctx, TaskDDNS, "RD Gateway address updater",
			ctl, `keepalive -config "`+ConfigPath(c.InstallRoot)+`"`, c.InstallRoot); err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		if err := StartTask(ctx, TaskDDNS); err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		report.step("Publishing", StepDone, "asking the router to forward 443 and keeping "+c.PublicDomain+" up to date")

	case FrontDoorRelay:
		report.step("Publishing", StepRunning, "allowing the relay in")
		if err := AllowRelay(ctx, caddy.Exe, c.CaddyPort); err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		script, err := VPSScript(c)
		if err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		relayScript = script
		report.step("Publishing", StepDone, "this PC is ready; the server still needs setting up")

	default:
		report.step("Publishing", StepRunning, "making the gateway reachable")
		ts, err := FindTailscale()
		if err != nil {
			report.step("Publishing", StepFailed, "Tailscale not found")
			return nil, err
		}
		if err := ts.EnableFunnel(ctx, c.CaddyPort); err != nil {
			report.step("Publishing", StepFailed, err.Error())
			return nil, err
		}
		report.step("Publishing", StepDone, c.PortalURL())
	}

	// --- Client helper ----------------------------------------------------
	report.step("Client configuration", StepRunning, "")
	clientIni := ClientINI(c.PublicHost(), `\.\`+os.Getenv("USERNAME"), nil)
	if err := os.WriteFile(winJoin(c.InstallRoot, "RDGHelper.ini"), []byte(clientIni), 0o644); err != nil {
		return nil, err
	}
	report.step("Client configuration", StepDone, "RDGHelper.ini written")

	if err := Save(c); err != nil {
		return nil, fmt.Errorf("saving configuration: %w", err)
	}

	return &Result{
		Config:        c,
		AdminPassword: password,
		TwoFactorURL:  c.TwoFactorURL(),
		RelayScript:   relayScript,
	}, nil
}

// ctlPath finds rdgwctl.exe, which the address-updater task runs. The installer
// puts it beside the desktop app; when this code is itself running inside
// rdgwctl there is nothing to look for.
func ctlPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := winDir(self)
	for _, cand := range []string{winJoin(dir, "rdgwctl.exe"), self} {
		base := strings.ToLower(baseName(cand))
		if !strings.HasPrefix(base, "rdgwctl") {
			continue
		}
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", errors.New("rdgwctl.exe was not found next to this program; reinstall the app")
}

func winDir(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[:i]
	}
	return "."
}

// RegisterGateway (re)writes the gateway's scheduled task. It runs through cmd
// so rdpgw's output lands in rdpgw\rdpgw.log: a scheduled task has no console,
// so without this a failed start leaves nothing to read. Used by setup and by
// the Repair action, which is how an older install gets the log.
func RegisterGateway(ctx context.Context, installRoot string) error {
	rdpgwDir := winJoin(installRoot, "rdpgw")
	rdpgwExe := winJoin(rdpgwDir, "rdpgw.exe")
	gwLog := winJoin(rdpgwDir, "rdpgw.log")
	gwCmd := `/c ""` + rdpgwExe + `" --conf "` + winJoin(rdpgwDir, "rdpgw.yaml") + `" >> "` + gwLog + `" 2>&1"`
	return RegisterTask(ctx, TaskGateway, "RD Gateway", "cmd.exe", gwCmd, rdpgwDir)
}

// Command rdgwctl exercises the provisioning core from a terminal.
//
// It exists so the logic can be verified before the Wails UI is written, and
// afterwards as the thing to reach for when the UI is the suspect rather than
// the stack. Every subcommand here maps to something the UI will call.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maxlieb/rdgw-manager/internal/provision"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var err error
	switch os.Args[1] {
	case "status":
		err = cmdStatus(ctx, os.Args[2:])
	case "render":
		err = cmdRender(os.Args[2:])
	case "tailscale":
		err = cmdTailscale(ctx)
	case "install":
		err = cmdInstall(ctx, os.Args[2:])
	case "uninstall":
		err = cmdUninstall(ctx, os.Args[2:])
	case "keepalive":
		// Runs until stopped, so it must not inherit the two-minute limit.
		err = cmdKeepalive(context.Background(), os.Args[2:])
	case "check-direct":
		err = cmdCheckDirect(ctx)
	case "relay-script":
		err = cmdRelayScript(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `rdgwctl - RD Gateway manager core

  status     [-config FILE] [-json]   show the dashboard checks
  render     -host NAME [-target H:P] print the configs without writing them
  tailscale                           show what Tailscale reports
  install    -target H:P [-door D]    provision everything (needs elevation)
  check-direct                        can this connection be reached directly?
  relay-script [-config FILE]         print the setup script for the relay server
  keepalive  [-config FILE]           (background task) keep port + DNS current
  uninstall  [-config FILE] [-purge]  remove services, optionally delete data

"render" touches nothing on disk. Use it to read the generated rdpgw.yaml and
app.ini before anything is installed.

"install" expects rdpgw.exe to already be at <root>\rdpgw\rdpgw.exe, since
upstream publishes no Windows build:
  GOOS=windows GOARCH=amd64 go build -o rdpgw.exe ./cmd/rdpgw

Three ways in, chosen with -door:

  funnel (default)  Tailscale Funnel. Free, no domain, works behind CGNAT.
                    The .ts.net name is something filtering products can
                    categorise.
  direct            Your own name, served from this PC. Needs a routable
                    address: the router forwards 443 (automatically over UPnP
                    where it allows) and a DNS updater keeps the name current.
                      -domain H  and either  -duckdns NAME:TOKEN  or  -ddns-url URL
  relay             A small public server holds the domain and forwards over
                    Tailscale. Nothing is opened on the router. Writes
                    relay-setup.sh, to run on the server.
                      -domain H
`)
}

func defaultConfigPath() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = "."
	}
	return filepath.Join(root, "RDGateway", "config.json")
}

func loadConfig(path string) (provision.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return provision.Config{}, provision.ErrNotProvisioned
		}
		return provision.Config{}, err
	}
	var c provision.Config
	if err := json.Unmarshal(b, &c); err != nil {
		return provision.Config{}, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return c, nil
}

func cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	path := fs.String("config", defaultConfigPath(), "path to config.json")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*path)
	if err != nil && err != provision.ErrNotProvisioned {
		return err
	}
	if err == provision.ErrNotProvisioned {
		// Still worth probing: the point of the dashboard is to explain what
		// is missing, and "nothing is installed" is a legitimate answer.
		fmt.Println("No installation found - reporting what can be seen anyway.")
		cfg.GiteaPort = provision.DefaultGiteaPort
		cfg.RdpgwPort = provision.DefaultRdpgwPort
	}

	st := provision.Gather(ctx, cfg)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}

	if st.Hostname != "" {
		fmt.Printf("\n  Portal: https://%s/\n", st.Hostname)
	}
	fmt.Println()
	for _, c := range st.Checks {
		fmt.Printf("  %-4s %-18s %s\n", marker(c.State), c.Name, c.Detail)
		if c.Hint != "" && c.State != provision.StateOK {
			for _, line := range wrap(c.Hint, 62) {
				fmt.Printf("       %s\n", line)
			}
		}
	}
	fmt.Println()
	if st.Healthy() {
		fmt.Println("  Everything is up.")
	}
	return nil
}

func marker(s provision.CheckState) string {
	switch s {
	case provision.StateOK:
		return "[ok]"
	case provision.StateWarn:
		return "[! ]"
	case provision.StateFail:
		return "[XX]"
	default:
		return "[? ]"
	}
}

// wrap keeps hints readable in a terminal without pulling in a dependency.
func wrap(s string, width int) []string {
	var lines []string
	var cur string
	for _, w := range strings.Fields(s) {
		if cur == "" {
			cur = w
			continue
		}
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func cmdRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	host := fs.String("host", "", "MagicDNS hostname, e.g. pc.tailnet.ts.net")
	root := fs.String("root", `C:\ProgramData\RDGateway`, "install root")
	var targets multiFlag
	fs.Var(&targets, "target", "RDP target as host:port (repeatable)")
	_ = fs.Parse(args)

	if *host == "" {
		return fmt.Errorf("-host is required")
	}
	cfg, err := provision.NewConfig(*root)
	if err != nil {
		return err
	}
	cfg.Hostname = *host
	cfg.Targets = targets
	if len(cfg.Targets) == 0 {
		cfg.Targets = []string{"localhost:3389"}
	}
	// Placeholders: rendering is for review, so it must not require Gitea to
	// have been contacted first.
	cfg.ClientID = "<created-when-you-install>"
	cfg.ClientSecret = "<created-when-you-install>"

	yaml, err := provision.RdpgwYAML(cfg)
	if err != nil {
		return err
	}
	secret, err := provision.NewPassword(64)
	if err != nil {
		return err
	}
	ini, err := provision.GiteaAppIni(cfg, secret, "")
	if err != nil {
		return err
	}

	fmt.Printf("===== rdpgw.yaml =====\n\n%s\n", yaml)
	fmt.Printf("===== gitea app.ini =====\n\n%s\n", ini)
	fmt.Printf("===== default.rdp =====\n\n%s\n", provision.DefaultRDP())
	fmt.Printf("===== RDGHelper.ini (client) =====\n\n%s\n",
		provision.ClientINI(cfg.Hostname, `\.\`+os.Getenv("USERNAME"), nil))
	return nil
}

func cmdTailscale(ctx context.Context) error {
	ts, err := provision.FindTailscale()
	if err != nil {
		return err
	}
	fmt.Println("binary:", ts.Exe)
	st, err := ts.State(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("state:      %s\n", st.Raw)
	fmt.Printf("logged in:  %t\n", st.LoggedIn)
	fmt.Printf("magic dns:  %t\n", st.MagicDNSEnabled)
	fmt.Printf("hostname:   %s\n", st.Hostname)
	fmt.Printf("tailnet:    %s\n", st.Tailnet)

	active, raw, err := ts.FunnelActive(ctx)
	if err != nil {
		fmt.Println("funnel:     could not read:", err)
		return nil
	}
	fmt.Printf("funnel:     %t\n", active)
	if raw != "" {
		fmt.Printf("\n%s\n", raw)
	}
	return nil
}

// printProgress renders step events as they arrive. The Wails UI will pass a
// function that forwards the same events to the frontend instead.
func printProgress(e provision.Event) {
	switch e.State {
	case provision.StepRunning:
		if e.Message != "" {
			fmt.Printf("  ... %-32s %s\n", e.Step, e.Message)
		} else {
			fmt.Printf("  ... %s\n", e.Step)
		}
	case provision.StepDone:
		fmt.Printf("  [ok] %-32s %s\n", e.Step, e.Message)
	case provision.StepSkipped:
		fmt.Printf("  [--] %-32s %s\n", e.Step, e.Message)
	case provision.StepFailed:
		fmt.Printf("  [XX] %-32s %s\n", e.Step, e.Message)
	}
}

func cmdInstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	root := fs.String("root", filepath.Join(os.Getenv("ProgramData"), "RDGateway"), "install root")
	version := fs.String("gitea-version", "", "Gitea version to download (default: built-in)")
	door := fs.String("door", "", "funnel (default), direct or relay")
	domain := fs.String("domain", "", "your own hostname, e.g. rdgw.example.com (direct, relay)")
	email := fs.String("email", "", "address for Let's Encrypt expiry notices")
	duck := fs.String("duckdns", "", "NAME:TOKEN for a DuckDNS name (direct)")
	ddnsURL := fs.String("ddns-url", "", "any provider's https update URL (direct)")
	var targets multiFlag
	fs.Var(&targets, "target", "RDP target as host:port (repeatable)")
	_ = fs.Parse(args)

	if len(targets) == 0 {
		return fmt.Errorf("at least one -target is required; the gateway refuses anything not listed")
	}
	// -domain on its own used to mean "own domain, directly"; keep that.
	if *door == "" {
		if *domain != "" {
			*door = "direct"
		} else {
			*door = "funnel"
		}
	}

	cfg, err := provision.NewConfig(*root)
	if err != nil {
		return err
	}
	cfg.Targets = targets
	if *version != "" {
		cfg.GiteaVersion = *version
	}
	cfg.ACMEEmail = *email

	switch *door {
	case "funnel":
		// The hostname is discovered rather than asked for: it is whatever
		// Tailscale calls this machine, and a typo would produce a gateway
		// that cannot issue a working .rdp.
		ts, err := provision.FindTailscale()
		if err != nil {
			return fmt.Errorf("Tailscale is not installed; install it and sign in")
		}
		fmt.Println("Waiting for Tailscale...")
		st, err := ts.WaitForBackend(ctx, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("%w (run 'tailscale up' and complete the browser sign-in)", err)
		}
		cfg.Hostname = st.Hostname
		fmt.Printf("Machine name: %s\n\n", st.Hostname)

	case "direct":
		if *domain == "" {
			return fmt.Errorf("-door direct needs -domain")
		}
		cfg.FrontDoor = provision.FrontDoorDirect
		cfg.PublicDomain = *domain
		switch {
		case *duck != "":
			name, token, ok := strings.Cut(*duck, ":")
			if !ok {
				return fmt.Errorf("-duckdns wants NAME:TOKEN")
			}
			if cfg.DDNSURL, err = provision.DuckDNSURL(name, token); err != nil {
				return err
			}
		case *ddnsURL != "":
			cfg.DDNSURL = *ddnsURL
		}
		fmt.Println("Checking the connection...")
		chk := provision.CheckDirect(ctx)
		fmt.Println(" ", chk.Reason)
		if chk.RouterFound && !chk.Public {
			return fmt.Errorf("this connection cannot be reached directly; use -door funnel or -door relay")
		}
		fmt.Println()

	case "relay":
		if *domain == "" {
			return fmt.Errorf("-door relay needs -domain")
		}
		cfg.FrontDoor = provision.FrontDoorRelay
		cfg.PublicDomain = *domain
		// Needs Tailscale up so the server has somewhere to send traffic.
		ts, err := provision.FindTailscale()
		if err != nil {
			return fmt.Errorf("Tailscale is not installed; install it and sign in")
		}
		st, err := ts.WaitForBackend(ctx, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("%w (run 'tailscale up' and complete the browser sign-in)", err)
		}
		cfg.Hostname = st.Hostname
		if cfg.TailscaleIP, err = ts.IPv4(ctx); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unknown -door %q (funnel, direct or relay)", *door)
	}

	res, err := provision.Install(ctx, cfg, printProgress)
	if err != nil {
		return err
	}

	fmt.Printf(`
  Done.

  Portal   %s
  Sign-in  %s

  Username %s
  Password %s

  Write that password down now - it is shown once and not stored.

  Last step: open the sign-in URL and set up your authenticator app.
  Enrollment is enforced, so the portal will not let you past until you do.
  %s
`,
		res.Config.PortalURL(), res.Config.Issuer(),
		res.Config.AdminUser, res.AdminPassword, res.TwoFactorURL)

	if res.RelayScript != "" {
		path := filepath.Join(*root, "relay-setup.sh")
		if err := os.WriteFile(path, []byte(res.RelayScript), 0o644); err != nil {
			return err
		}
		fmt.Printf(`
  This PC is ready, but the relay server still needs setting up.
  Copy this file to the server and run it there:

    %s
    bash relay-setup.sh

  Then point an A record for %s at the server's public address.
`, path, res.Config.PublicDomain)
	}
	return nil
}

func cmdCheckDirect(ctx context.Context) error {
	r := provision.CheckDirect(ctx)
	fmt.Println(r.Reason)
	if r.RouterFound && !r.Public {
		os.Exit(3)
	}
	return nil
}

func cmdRelayScript(args []string) error {
	fs := flag.NewFlagSet("relay-script", flag.ExitOnError)
	path := fs.String("config", defaultConfigPath(), "path to config.json")
	_ = fs.Parse(args)
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	out, err := provision.VPSScript(cfg)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func cmdKeepalive(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("keepalive", flag.ExitOnError)
	path := fs.String("config", defaultConfigPath(), "path to config.json")
	_ = fs.Parse(args)
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	provision.KeepReachable(ctx, cfg, 5*time.Minute, func(m string) {
		fmt.Println(time.Now().Format("15:04:05"), m)
	})
	return nil
}

func cmdUninstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	path := fs.String("config", defaultConfigPath(), "path to config.json")
	purge := fs.Bool("purge", false, "also delete the install folder, including the sign-in account and its TOTP enrollment")
	keepFunnel := fs.Bool("keep-funnel", false, "leave the machine published (not recommended)")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*path)
	if err != nil && err != provision.ErrNotProvisioned {
		return err
	}
	if err == provision.ErrNotProvisioned {
		fmt.Println("No saved configuration; removing whatever is registered anyway.")
		cfg.InstallRoot = ""
	}

	opts := provision.UninstallOptions{KeepData: !*purge, ResetFunnel: !*keepFunnel}
	if *purge && cfg.InstallRoot == "" {
		return fmt.Errorf("-purge needs a saved config so it knows what to delete")
	}

	errs := provision.Uninstall(ctx, cfg, opts, printProgress)
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "\nFinished with problems:")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  -", e)
		}
		return fmt.Errorf("%d step(s) failed", len(errs))
	}
	fmt.Println("\n  Removed.")
	if !*purge {
		fmt.Println("  Settings and the sign-in account were kept. Use -purge to delete them.")
	}
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

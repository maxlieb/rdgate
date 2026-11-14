package provision

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Tailscale wraps the CLI. Driving the binary rather than the local API keeps
// this honest about what the user could reproduce by hand, which matters when
// something goes wrong and the dashboard has to explain itself.
type Tailscale struct {
	Exe string
}

// FindTailscale locates tailscale.exe, or returns an error the UI can turn
// into an "install Tailscale" step rather than a stack trace.
func FindTailscale() (*Tailscale, error) {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Tailscale", "tailscale.exe"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return &Tailscale{Exe: c}, nil
		}
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return &Tailscale{Exe: p}, nil
	}
	return nil, errors.New("tailscale not found")
}

// tsStatus mirrors only the handful of fields we actually read out of
// `tailscale status --json`, so upstream additions cannot break decoding.
type tsStatus struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName string `json:"DNSName"`
		Online  bool   `json:"Online"`
	} `json:"Self"`
	CurrentTailnet struct {
		Name            string `json:"Name"`
		MagicDNSSuffix  string `json:"MagicDNSSuffix"`
		MagicDNSEnabled bool   `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
}

// TailscaleState is the digested form the dashboard renders.
type TailscaleState struct {
	Running         bool
	LoggedIn        bool
	MagicDNSEnabled bool
	Hostname        string // fully qualified, trailing dot stripped
	Tailnet         string
	Raw             string // BackendState, for the detail line
}

func (t *Tailscale) run(ctx context.Context, args ...string) (string, error) {
	cmd := Command(ctx, t.Exe, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// State reports what Tailscale is currently doing.
func (t *Tailscale) State(ctx context.Context) (TailscaleState, error) {
	out, err := t.run(ctx, "status", "--json")
	if err != nil {
		// A non-zero exit still usually carries usable JSON (for example when
		// logged out), so attempt to decode before giving up.
		if out == "" {
			return TailscaleState{}, err
		}
	}
	var s tsStatus
	if jsonErr := json.Unmarshal([]byte(out), &s); jsonErr != nil {
		return TailscaleState{}, errors.New("could not parse tailscale status: " + jsonErr.Error())
	}

	st := TailscaleState{
		Running:         s.BackendState == "Running",
		LoggedIn:        s.BackendState != "NeedsLogin" && s.BackendState != "NoState",
		MagicDNSEnabled: s.CurrentTailnet.MagicDNSEnabled,
		Hostname:        strings.TrimSuffix(s.Self.DNSName, "."),
		Tailnet:         s.CurrentTailnet.Name,
		Raw:             s.BackendState,
	}
	return st, nil
}

// IPv4 is this machine's tailnet address.
func (t *Tailscale) IPv4(ctx context.Context) (string, error) {
	out, err := t.run(ctx, "ip", "-4")
	if err != nil {
		return "", errors.New("could not read the Tailscale address: " + out)
	}
	ip := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	return validateIPv4(ip)
}

// EnableFunnel publishes both services.
//
// The gateway uses TLS-terminated TCP rather than the HTTP proxy: RD Gateway
// speaks non-standard verbs (RDG_IN_DATA / RDG_OUT_DATA) that an HTTP-aware
// proxy has no reason to understand. Terminating TLS and forwarding raw TCP
// sidesteps the question entirely. Gitea is ordinary HTTP and needs no such
// treatment.
//
// Funnel must be permitted for this node in the tailnet policy file
// (nodeAttrs -> "funnel"); without it these commands fail with a message
// pointing at the admin console, which the caller should surface verbatim.
func (t *Tailscale) EnableFunnel(ctx context.Context, caddyPort int) error {
	// One port, not two. Caddy behind it splits the gateway from the sign-in
	// service by path, so a client that is only allowed outbound 443 can
	// still reach both - which an 8443 sign-in URL quietly prevents.
	args := []string{"funnel", "--bg",
		"--tls-terminated-tcp=" + itoa(PublicPort),
		"tcp://127.0.0.1:" + itoa(caddyPort)}
	// Bounded: when Funnel is not enabled for the tailnet, the command can hang
	// instead of failing, and the wizard then sits on "making the gateway reachable".
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := t.run(ctx, args...)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &FunnelError{URL: firstURL(out), Msg: "Tailscale did not finish publishing on 443. Funnel is not allowed for this PC yet."}
	}
	if err != nil {
		return &FunnelError{URL: firstURL(out), Msg: "publishing on 443 failed: " + out}
	}
	return nil
}

// FunnelError is returned when Tailscale refuses to publish. URL is the page
// the CLI printed for allowing Funnel on this PC, when it printed one.
type FunnelError struct {
	URL string
	Msg string
}

func (e *FunnelError) Error() string { return e.Msg }

var urlRe = regexp.MustCompile(`https://[^\s"'<>]+`)

func firstURL(s string) string { return urlRe.FindString(s) }

// FunnelReadiness is what the Funnel step shows. Each field is one switch in
// the Tailscale admin console, so the wizard can point at exactly the one that
// is off.
type FunnelReadiness struct {
	Hostname    string `json:"hostname"`
	Connected   bool   `json:"connected"`
	MagicDNS    bool   `json:"magicDNS"`
	HTTPS       bool   `json:"https"`
	HTTPSDetail string `json:"httpsDetail"`
	FunnelOn    bool   `json:"funnelOn"`
	FunnelURL   string `json:"funnelURL"`
}

// Readiness checks MagicDNS and HTTPS certificates directly. Funnel permission
// cannot be read without asking Tailscale to publish, so FunnelOn reports only
// whether something is already published.
func (t *Tailscale) Readiness(ctx context.Context) (FunnelReadiness, error) {
	st, err := t.State(ctx)
	if err != nil {
		return FunnelReadiness{}, err
	}
	r := FunnelReadiness{Hostname: st.Hostname, Connected: st.Running, MagicDNS: st.MagicDNSEnabled}
	if !st.Running || st.Hostname == "" {
		r.HTTPSDetail = "This PC is not connected to your tailnet yet."
		return r, nil
	}

	dir, err := os.MkdirTemp("", "rdgw-cert-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(dir)
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := t.run(cctx, "cert",
		"--cert-file", filepath.Join(dir, "c.crt"),
		"--key-file", filepath.Join(dir, "c.key"),
		st.Hostname)
	if err == nil {
		r.HTTPS = true
	} else {
		r.HTTPSDetail = firstLine(out)
	}

	on, _, _ := t.FunnelActive(ctx)
	r.FunnelOn = on
	return r, nil
}

// FunnelActive reports whether anything is currently published. Used by the
// dashboard rather than by provisioning.
func (t *Tailscale) FunnelActive(ctx context.Context) (bool, string, error) {
	out, err := t.run(ctx, "funnel", "status")
	if err != nil {
		return false, out, err
	}
	return funnelOutputActive(out), out, nil
}

// funnelOutputActive reads `tailscale funnel status`. We publish TCP with TLS
// termination, which prints "tcp://<name>:443 (TLS terminated)" and no
// "https://" line, so both forms count. An unconfigured node prints nothing
// or "No serve config".
func funnelOutputActive(out string) bool {
	if out == "" || strings.Contains(strings.ToLower(out), "no serve config") {
		return false
	}
	return strings.Contains(out, "https://") ||
		strings.Contains(out, "TLS terminated") ||
		strings.Contains(out, "Funnel on")
}

// ResetFunnel withdraws everything this tool published. Called on uninstall.
func (t *Tailscale) ResetFunnel(ctx context.Context) error {
	_, err := t.run(ctx, "funnel", "reset")
	return err
}

// WaitForBackend polls until Tailscale reports Running, so the wizard can show
// "waiting for you to finish signing in" instead of failing immediately.
func (t *Tailscale) WaitForBackend(ctx context.Context, timeout time.Duration) (TailscaleState, error) {
	deadline := time.Now().Add(timeout)
	var last TailscaleState
	for time.Now().Before(deadline) {
		st, err := t.State(ctx)
		if err == nil {
			last = st
			if st.Running && st.Hostname != "" {
				return st, nil
			}
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return last, errors.New("timed out waiting for Tailscale to come up")
}

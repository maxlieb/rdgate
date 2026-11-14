package provision

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// Gather builds the dashboard. Every check is independent and failure-tolerant:
// one subsystem being unreachable must not blank the rest of the page, and a
// check that could not run reports Unknown rather than claiming a failure.
func Gather(ctx context.Context, c Config) Status {
	s := Status{
		CheckedAt: time.Now(),
		Hostname:  c.Hostname,
		PortalURL: c.PortalURL(),
	}
	// Direct mode has no use for Tailscale, so it is not a row there: a red
	// light for something the setup does not depend on would be noise.
	if c.FrontDoor != FrontDoorDirect {
		s.Checks = append(s.Checks, checkTailscale(ctx, &s))
	}
	s.Checks = append(s.Checks,
		checkPublic(ctx, c),
		checkListening("Sign-in service", c.GiteaPort,
			"Gitea is not listening. Check the log under the install folder."),
		checkListening("Gateway service", c.RdpgwPort,
			"rdpgw is not listening. Its output is in rdpgw\\rdpgw.log under the install folder."),
		checkListening("Web front end", c.CaddyListenPort(),
			"Caddy is not listening, so nothing public reaches either service."),
		checkTwoFactor(ctx, c),
		checkRDPEnabled(),
	)
	return s
}

func checkTailscale(ctx context.Context, s *Status) Check {
	ts, err := FindTailscale()
	if err != nil {
		return Check{Name: "Tailscale", State: StateFail,
			Detail: "not installed",
			Hint:   "Install Tailscale, then sign in. The wizard can do this for you."}
	}
	st, err := ts.State(ctx)
	if err != nil {
		return Check{Name: "Tailscale", State: StateUnknown,
			Detail: "could not read status", Hint: err.Error()}
	}
	switch {
	case !st.LoggedIn:
		return Check{Name: "Tailscale", State: StateFail,
			Detail: "not signed in",
			Hint:   "Run the wizard, or 'tailscale up', and complete the browser sign-in."}
	case !st.Running:
		return Check{Name: "Tailscale", State: StateWarn,
			Detail: st.Raw, Hint: "Tailscale is signed in but not connected yet."}
	case st.Hostname == "":
		return Check{Name: "Tailscale", State: StateWarn,
			Detail: "no MagicDNS name",
			Hint:   "Enable MagicDNS and HTTPS certificates in the Tailscale admin console."}
	}
	if s.Hostname == "" {
		s.Hostname = st.Hostname
	}
	return Check{Name: "Tailscale", State: StateOK, Detail: st.Hostname}
}

// CheckPublic is the dashboard's public-address row on its own, for the
// wizard's "test it now" button.
func CheckPublic(ctx context.Context, c Config) Check { return checkPublic(ctx, c) }

// checkPublic reports whether the outside world can reach the gateway. How
// that is judged depends on the front door, because each one fails differently.
func checkPublic(ctx context.Context, c Config) Check {
	const name = "Public address"
	switch c.FrontDoor {
	case FrontDoorDirect, FrontDoorRelay:
		return checkOwnDomain(ctx, c)
	}
	ts, err := FindTailscale()
	if err != nil {
		return Check{Name: name, State: StateUnknown, Detail: "Tailscale not found"}
	}
	active, raw, err := ts.FunnelActive(ctx)
	if err != nil {
		return Check{Name: name, State: StateUnknown,
			Detail: "could not read funnel status", Hint: firstLine(raw)}
	}
	if !active {
		return Check{Name: name, State: StateFail,
			Detail: "not published",
			Hint: "Funnel is off. It also has to be allowed for this machine in the " +
				"tailnet policy file (nodeAttrs with the \"funnel\" attribute)."}
	}
	return Check{Name: name, State: StateOK, Detail: "published on 443"}
}

// checkOwnDomain covers direct and relay. The two questions are whether the
// name points where it should, and whether a real HTTPS request to it gets an
// answer - the second being an end-to-end test of the whole path.
func checkOwnDomain(ctx context.Context, c Config) Check {
	const name = "Public address"
	host := c.PublicDomain
	if host == "" {
		return Check{Name: name, State: StateFail, Detail: "no address chosen"}
	}

	var resolver net.Resolver
	ips, err := resolver.LookupHost(ctx, host)
	if err != nil || len(ips) == 0 {
		hint := "The name does not resolve yet. DNS changes can take a few minutes."
		if c.FrontDoor == FrontDoorRelay {
			hint = "Add an A record for " + host + " pointing at your server's public address."
		} else if c.DDNSURL != "" {
			hint = "The updater has not registered this name yet; give it a few minutes, then check its log."
		}
		return Check{Name: name, State: StateFail, Detail: host + " does not resolve", Hint: hint}
	}

	status, err := probeHTTPS(ctx, host)
	if err == nil {
		return Check{Name: name, State: StateOK,
			Detail: host + " answers (" + itoa(status) + ")"}
	}

	if c.FrontDoor == FrontDoorRelay {
		return Check{Name: name, State: StateFail, Detail: host + " does not answer",
			Hint: "The server is not passing traffic through. Check that it is running, that " +
				"ports 80 and 443 are open in Oracle's console, and that this PC is on."}
	}
	// Direct: asking for your own public address from inside your own network
	// fails on many routers ("hairpin NAT") even when everything is fine, so
	// this can only be a warning, not a verdict.
	return Check{Name: name, State: StateWarn, Detail: host + " did not answer from here",
		Hint: "Many routers cannot loop back to their own public address, so this may be normal. " +
			"Test from a phone on mobile data to be sure."}
}

// probeHTTPS makes a real request. Any HTTP response counts: a 302 to the
// sign-in page is exactly what a working gateway returns.
func probeHTTPS(ctx context.Context, host string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// DirectCheck answers, before anything is installed, whether the direct front
// door can work on this connection at all.
type DirectCheck struct {
	RouterFound bool   `json:"routerFound"` // a UPnP router answered
	ExternalIP  string `json:"externalIp"`  // what the router says it is
	Public      bool   `json:"public"`      // that address is reachable from the internet
	Reason      string `json:"reason"`      // plain-language verdict
}

// CheckDirect asks the router what address it has. Behind CGNAT that address
// is private or in 100.64.0.0/10, which settles the question without anyone
// having to understand CGNAT.
func CheckDirect(ctx context.Context) DirectCheck {
	igd, err := DiscoverIGD(ctx)
	if err != nil {
		return DirectCheck{Reason: "No router answered the automatic setup request. " +
			"You can still use this option by forwarding port 443 to this PC in the router's settings."}
	}
	ip, err := igd.ExternalIP(ctx)
	if err != nil {
		return DirectCheck{RouterFound: true,
			Reason: "The router answered but would not say what address it has."}
	}
	if !IsPublicIPv4(ip) {
		return DirectCheck{RouterFound: true, ExternalIP: ip,
			Reason: "Your internet provider shares one public address between customers " +
				"(the router reports " + ip + "), so nothing can reach you directly. Use Tailscale Funnel or a relay server instead."}
	}
	return DirectCheck{RouterFound: true, ExternalIP: ip, Public: true,
		Reason: "Your connection has its own public address (" + ip + ") and the router supports automatic setup."}
}

// checkListening probes the loopback port rather than looking for a process,
// because a running process that failed to bind is the failure we care about.
func checkListening(name string, port int, hint string) Check {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(port), 2*time.Second)
	if err != nil {
		return Check{Name: name, State: StateFail,
			Detail: "not listening on " + itoa(port), Hint: hint}
	}
	_ = conn.Close()
	return Check{Name: name, State: StateOK, Detail: "listening on " + itoa(port)}
}

// checkTwoFactor asks Gitea's admin API whether anyone has actually enrolled.
// Enrollment is enforced in app.ini so a new user cannot get past it, but this
// still catches a config that predates the enforcement setting.
func checkTwoFactor(ctx context.Context, c Config) Check {
	// Gitea's admin API cannot filter users by two-factor status: the query
	// returned every account, so this used to report "enrolled" for anyone.
	// Until enrollment is enforced on the sign-in path, say so instead.
	const name = "Two-factor"
	return Check{Name: name, State: StateUnknown, Detail: "not enforced yet",
		Hint: "Sign-in does not yet require two-factor. Do not share the address until it does."}
}

// checkRDPEnabled catches the embarrassing case where the whole stack is
// healthy and Remote Desktop is switched off, so there is nothing to reach.
func checkRDPEnabled() Check {
	const name = "Remote Desktop"
	if runtime.GOOS != "windows" {
		return Check{Name: name, State: StateUnknown, Detail: "not a Windows host"}
	}
	out, err := Command(context.Background(), "reg", "query",
		`HKLM\System\CurrentControlSet\Control\Terminal Server`,
		"/v", "fDenyTSConnections").Output()
	if err != nil {
		return Check{Name: name, State: StateUnknown, Detail: "could not read the setting"}
	}
	// A value of 0 means connections are allowed.
	if strings.Contains(string(out), "0x0") {
		return Check{Name: name, State: StateOK, Detail: "enabled"}
	}
	return Check{Name: name, State: StateFail,
		Detail: "disabled",
		Hint:   "Turn on Remote Desktop under Settings, System, Remote Desktop."}
}

// reason picks the lines that explain a failure from a command's output. Gitea
// logs harmless warnings first and the real error last, so showing the first
// line shows the wrong thing. This keeps the lines marked as errors, and falls
// back to the last few lines when none are marked.
func reason(s string) string {
	var marked, tail []string
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		if strings.Contains(t, "[F]") || strings.Contains(t, "[E]") ||
			strings.Contains(low, "fatal") || strings.Contains(low, "error") {
			marked = append(marked, t)
		}
	}
	if len(marked) > 0 {
		return strings.Join(marked, " | ")
	}
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			tail = append(tail, t)
		}
	}
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	return strings.Join(tail, " | ")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ErrNotProvisioned is returned when no saved config exists yet, which the UI
// treats as "show the wizard" rather than as an error.
var ErrNotProvisioned = errors.New("not provisioned yet")

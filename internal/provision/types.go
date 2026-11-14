// Package provision stands up and inspects a self-hosted RD Gateway on this
// machine: rdpgw as the gateway, Gitea as the OIDC provider, and Tailscale
// Funnel as the public front door.
//
// Everything here is deliberately dependency-free (standard library only) so
// the core can be built and tested independently of the Wails UI that will sit
// on top of it. The UI calls these same functions; nothing is CLI-specific.
package provision

import "time"

// Ports used on the loopback interface. Nothing here listens publicly - Funnel
// is the only thing bound to the outside world.
const (
	DefaultRdpgwPort = 8080
	DefaultGiteaPort = 3000

	// Caddy sits in front of both services and is the only thing the front
	// door talks to. It listens on loopback in funnel mode and on :443 in
	// domain mode.
	DefaultCaddyPort = 8081

	// Everything public is on 443. An earlier design put the sign-in service
	// on Funnel's 8443, which is legitimate for Funnel but dies behind a
	// corporate firewall that only permits outbound 443 - and a managed
	// client is exactly who this is for.
	PublicPort = 443

	// AuthPath is where the sign-in service lives in funnel mode, which has
	// only one hostname to work with. No trailing slash: it is concatenated
	// into an issuer, and rdpgw compares that string exactly.
	AuthPath = "/auth"
)

// FrontDoor selects what makes the gateway reachable. There are three, and
// they differ in who carries the traffic, not in what runs on this machine:
// the same Caddy, rdpgw and Gitea sit behind every one of them.
type FrontDoor string

const (
	// FrontDoorFunnel publishes through Tailscale. Free, works behind CGNAT,
	// and needs no domain - but the hostname announces what it is, which
	// filtering products categorise.
	FrontDoorFunnel FrontDoor = "funnel"

	// FrontDoorDirect serves a hostname straight from this machine: the router
	// forwards 443 here (UPnP when the router allows it) and a dynamic-DNS
	// updater keeps the name pointing at the home address. Free and the
	// simplest path, but it needs a routable address - behind CGNAT it cannot
	// work, and the app says so rather than letting someone debug it.
	FrontDoorDirect FrontDoor = "direct"

	// FrontDoorRelay puts a small public server (a free Oracle VM, say) in
	// front: it owns the domain and the certificate, and forwards over
	// Tailscale to this machine. Nothing is opened on the home router, CGNAT
	// is irrelevant, and the domain can be an unremarkable one - at the price
	// of someone setting the server up once.
	FrontDoorRelay FrontDoor = "relay"
)

// Config is everything needed to provision, and everything worth letting the
// user change afterwards. It is persisted as JSON next to the install.
type Config struct {
	InstallRoot string `json:"installRoot"`

	// FrontDoor decides how the outside world reaches this machine.
	FrontDoor FrontDoor `json:"frontDoor"`

	// Hostname is this machine's MagicDNS name, e.g. pc.tailnet.ts.net.
	// Discovered from Tailscale rather than entered by hand. Still recorded
	// in domain mode, because Tailscale often remains the way in from the
	// owner's own devices.
	Hostname string `json:"hostname"`

	// PublicDomain is the one name clients use in direct and relay modes. One
	// name rather than two, with sign-in under /auth, so that every front door
	// has the same shape: one hostname, one certificate, one Gitea
	// configuration to get right instead of three.
	PublicDomain string `json:"publicDomain"`

	// ACMEEmail is where Let's Encrypt sends expiry warnings. Optional.
	ACMEEmail string `json:"acmeEmail"`

	// DDNSURL is fetched every few minutes in direct mode to keep PublicDomain
	// pointing at the home address. Any provider's update URL works; it holds
	// a token, so it is stored like the other secrets and never displayed.
	DDNSURL string `json:"ddnsUrl,omitempty"`

	// TailscaleIP is this machine's tailnet address. In relay mode the home
	// Caddy listens only on it, so the sign-in and gateway are reachable from
	// the relay and from nothing else.
	TailscaleIP string `json:"tailscaleIp,omitempty"`

	CaddyPort    int    `json:"caddyPort"`
	CaddyVersion string `json:"caddyVersion"`

	// Targets are the RDP endpoints the gateway will allow, as host:port.
	// rdpgw refuses anything not on this list, so it doubles as an allowlist.
	Targets []string `json:"targets"`

	AdminUser string `json:"adminUser"`

	RdpgwPort int `json:"rdpgwPort"`
	GiteaPort int `json:"giteaPort"`

	GiteaVersion string `json:"giteaVersion"`

	// OIDC client credentials, filled in once Gitea has been asked to create
	// the application. Secret is written to disk; the install directory is
	// expected to be ACL'd to administrators.
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`

	// GiteaAdminToken lets the manager read enrollment status back out of
	// Gitea. Scoped to read:admin - it is a status probe, not a management
	// channel, and it should never be minted with write scopes.
	GiteaAdminToken string `json:"giteaAdminToken"`

	// Keys must each be EXACTLY 32 characters. rdpgw silently substitutes a
	// random value for anything else at boot, which invalidates every session
	// on restart and presents as "it logs me out whenever the service blips".
	SessionKey           string `json:"sessionKey"`
	SessionEncryptionKey string `json:"sessionEncryptionKey"`
	PAATokenSigningKey   string `json:"paaTokenSigningKey"`
	PAATokenEncKey       string `json:"paaTokenEncryptionKey"`
}

// PublicHost is the name the remote client actually types.
func (c Config) PublicHost() string {
	if c.FrontDoor != FrontDoorFunnel && c.PublicDomain != "" {
		return c.PublicDomain
	}
	return c.Hostname
}

// Issuer is the OIDC issuer URL rdpgw validates against the discovery
// document. The absence of a trailing slash is load-bearing: rdpgw compares it
// to the provider's advertised issuer as an exact string.
//
// Sign-in lives under a path on the single public hostname in every mode.
func (c Config) Issuer() string {
	return "https://" + c.PublicHost() + AuthPath
}

// GiteaRootURL is the same place, with the trailing slash Gitea wants and
// rdpgw refuses.
func (c Config) GiteaRootURL() string { return c.Issuer() + "/" }

// PortalURL is what the user opens in a browser, and what goes in the .rdp.
func (c Config) PortalURL() string {
	return "https://" + c.PublicHost() + "/"
}

// RedirectURI is registered with Gitea as the OAuth2 callback.
func (c Config) RedirectURI() string {
	return "https://" + c.PublicHost() + "/callback"
}

// TwoFactorURL is where the user enrolls their authenticator. There is no API
// for enrollment, so the UI opens this page in an embedded browser.
func (c Config) TwoFactorURL() string {
	return c.Issuer() + "/user/settings/security/two_factor"
}

// NewConfig returns a Config with sane defaults and freshly generated keys.
func NewConfig(installRoot string) (Config, error) {
	keys := make([]string, 4)
	for i := range keys {
		k, err := NewKey32()
		if err != nil {
			return Config{}, err
		}
		keys[i] = k
	}
	return Config{
		InstallRoot:          installRoot,
		FrontDoor:            FrontDoorFunnel,
		AdminUser:            "rdgw",
		RdpgwPort:            DefaultRdpgwPort,
		GiteaPort:            DefaultGiteaPort,
		CaddyPort:            DefaultCaddyPort,
		GiteaVersion:         "1.24.3",
		CaddyVersion:         "2.10.2",
		SessionKey:           keys[0],
		SessionEncryptionKey: keys[1],
		PAATokenSigningKey:   keys[2],
		PAATokenEncKey:       keys[3],
	}, nil
}

// CheckState is a tri-state because "we could not tell" is a different thing
// from "it is broken", and the dashboard should not claim a red light when it
// simply failed to look.
type CheckState string

const (
	StateOK      CheckState = "ok"
	StateWarn    CheckState = "warn"
	StateFail    CheckState = "fail"
	StateUnknown CheckState = "unknown"
)

// Check is one row in the dashboard.
type Check struct {
	Name   string     `json:"name"`
	State  CheckState `json:"state"`
	Detail string     `json:"detail"`
	// Hint is shown only when the state is not OK: what the user should do,
	// in plain words, not an error code.
	Hint string `json:"hint,omitempty"`
}

// Status is the whole dashboard in one struct.
type Status struct {
	CheckedAt time.Time `json:"checkedAt"`
	Hostname  string    `json:"hostname"`
	PortalURL string    `json:"portalUrl"`
	Checks    []Check   `json:"checks"`
}

// Healthy reports whether every check passed, which is what decides whether
// the tray icon is green.
func (s Status) Healthy() bool {
	for _, c := range s.Checks {
		if c.State != StateOK {
			return false
		}
	}
	return true
}

// itoa avoids pulling strconv into the hot path of string building above; it
// is only ever called with small positive port numbers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

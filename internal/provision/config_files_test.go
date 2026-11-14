package provision

import (
	"strings"
	"testing"
	"time"
)

func validConfig(t *testing.T) Config {
	t.Helper()
	c, err := NewConfig(`C:\ProgramData\RDGateway`)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	c.Hostname = "pc.example-tailnet.ts.net"
	c.Targets = []string{"pc:3389"}
	c.ClientID = "client-id"
	c.ClientSecret = "client-secret"
	return c
}

func TestNewConfigGeneratesExactly32CharKeys(t *testing.T) {
	c, err := NewConfig(`C:\x`)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	for name, k := range map[string]string{
		"SessionKey":            c.SessionKey,
		"SessionEncryptionKey":  c.SessionEncryptionKey,
		"PAATokenSigningKey":    c.PAATokenSigningKey,
		"PAATokenEncryptionKey": c.PAATokenEncKey,
	} {
		if len(k) != 32 {
			t.Errorf("%s = %q (%d chars), want exactly 32", name, k, len(k))
		}
	}
}

func TestKeysAreDistinct(t *testing.T) {
	c, _ := NewConfig(`C:\x`)
	seen := map[string]bool{}
	for _, k := range []string{c.SessionKey, c.SessionEncryptionKey, c.PAATokenSigningKey, c.PAATokenEncKey} {
		if seen[k] {
			t.Fatalf("duplicate key generated: %q", k)
		}
		seen[k] = true
	}
}

func TestValidateKeysRejectsWrongLength(t *testing.T) {
	c := validConfig(t)
	c.SessionKey = strings.Repeat("a", 64) // the openssl rand -hex 32 mistake
	if err := ValidateKeys(c); err == nil {
		t.Fatal("expected a 64-char key to be rejected")
	}
}

func directConfig(t *testing.T) Config {
	t.Helper()
	c := validConfig(t)
	c.FrontDoor = FrontDoorDirect
	c.PublicDomain = "rdgw.example.com"
	return c
}

func relayConfig(t *testing.T) Config {
	t.Helper()
	c := directConfig(t)
	c.FrontDoor = FrontDoorRelay
	c.TailscaleIP = "100.101.102.103"
	return c
}

// The trailing slash is the detail that matters here. rdpgw compares
// ProviderUrl with the issuer in the discovery document as an exact string,
// and Gitea advertises its ROOT_URL, which ends in a slash. So ProviderUrl must
// be that same string, slash included. (Live log: expected ".../auth" got ".../auth/".)
func TestProviderURLMatchesGiteaIssuer(t *testing.T) {
	for name, c := range map[string]Config{
		"funnel": validConfig(t),
		"direct": directConfig(t),
		"relay":  relayConfig(t),
	} {
		if got := c.Issuer(); strings.HasSuffix(got, "/") {
			t.Errorf("%s: Issuer() = %q, must not end in a slash", name, got)
		}
		if got := c.GiteaRootURL(); !strings.HasSuffix(got, "/") {
			t.Errorf("%s: GiteaRootURL() = %q, must end in a slash", name, got)
		}
	}
}

func TestURLsPerFrontDoor(t *testing.T) {
	f := validConfig(t)
	if want := "https://pc.example-tailnet.ts.net/auth"; f.Issuer() != want {
		t.Errorf("funnel Issuer() = %q, want %q", f.Issuer(), want)
	}
	if want := "https://pc.example-tailnet.ts.net/"; f.PortalURL() != want {
		t.Errorf("funnel PortalURL() = %q, want %q", f.PortalURL(), want)
	}

	for name, d := range map[string]Config{"direct": directConfig(t), "relay": relayConfig(t)} {
		if want := "https://rdgw.example.com/auth"; d.Issuer() != want {
			t.Errorf("%s Issuer() = %q, want %q", name, d.Issuer(), want)
		}
		if want := "https://rdgw.example.com/"; d.PortalURL() != want {
			t.Errorf("%s PortalURL() = %q, want %q", name, d.PortalURL(), want)
		}
		if want := "https://rdgw.example.com/callback"; d.RedirectURI() != want {
			t.Errorf("%s RedirectURI() = %q, want %q", name, d.RedirectURI(), want)
		}
	}
}

// Nothing public may land on a port other than 443: a corporate client is
// often permitted outbound 443 and nothing else, which is the whole reason
// Caddy exists in this design.
func TestEverythingPublicIsOn443(t *testing.T) {
	for name, c := range map[string]Config{
		"funnel": validConfig(t),
		"direct": directConfig(t),
		"relay":  relayConfig(t),
	} {
		for _, u := range []string{c.PortalURL(), c.Issuer(), c.RedirectURI(), c.TwoFactorURL()} {
			if strings.Contains(strings.TrimPrefix(u, "https://"), ":") {
				t.Errorf("%s: %q carries an explicit port; it must be reachable on 443", name, u)
			}
		}
	}
}

func TestCaddyfileFunnelSplitsByPath(t *testing.T) {
	c := validConfig(t)
	out, err := Caddyfile(c)
	if err != nil {
		t.Fatalf("Caddyfile: %v", err)
	}
	for _, want := range []string{
		"auto_https off", // Tailscale already terminated TLS
		"http://:8081",
		"handle_path /auth/*",
		"reverse_proxy 127.0.0.1:3000",
		"reverse_proxy 127.0.0.1:8080",
		"admin off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("funnel Caddyfile missing %q\n---\n%s", want, out)
		}
	}
}

func TestCaddyfileDirectTerminatesTLSItself(t *testing.T) {
	c := directConfig(t)
	c.ACMEEmail = "max@example.com"
	out, err := Caddyfile(c)
	if err != nil {
		t.Fatalf("Caddyfile: %v", err)
	}
	for _, want := range []string{
		"email max@example.com",
		"rdgw.example.com {",
		"handle_path /auth/*",
		"reverse_proxy 127.0.0.1:8080",
		"reverse_proxy 127.0.0.1:3000",
		"flush_interval -1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("direct Caddyfile missing %q\n---\n%s", want, out)
		}
	}
	// Caddy must obtain certificates here, so auto_https must stay on.
	if strings.Contains(out, "auto_https off") {
		t.Error("direct mode needs automatic HTTPS; it was switched off")
	}
}

func TestCaddyfileDirectRequiresAName(t *testing.T) {
	c := directConfig(t)
	c.PublicDomain = ""
	if _, err := Caddyfile(c); err == nil {
		t.Error("expected an error when the hostname is missing")
	}
}

// The home listener in relay mode must be reachable from the tailnet alone. A
// bare ":8081" would also be on the LAN, and on Wi-Fi at a coffee shop.
func TestRelayHomeListenerIsBoundToTheTailnetAddress(t *testing.T) {
	out, err := Caddyfile(relayConfig(t))
	if err != nil {
		t.Fatalf("Caddyfile: %v", err)
	}
	for _, want := range []string{"bind 100.101.102.103", "auto_https off", "http://:8081"} {
		if !strings.Contains(out, want) {
			t.Errorf("relay Caddyfile missing %q\n---\n%s", want, out)
		}
	}
}

func TestFunnelListenerIsLoopbackOnly(t *testing.T) {
	out, _ := Caddyfile(validConfig(t))
	if !strings.Contains(out, "bind 127.0.0.1") {
		t.Errorf("funnel listener must bind loopback only\n%s", out)
	}
}

func TestRelayCaddyfileRequiresTailscaleAddress(t *testing.T) {
	c := relayConfig(t)
	c.TailscaleIP = ""
	if _, err := Caddyfile(c); err == nil {
		t.Error("expected an error without a tailnet address")
	}
}

func TestRdpgwYAMLContainsEssentials(t *testing.T) {
	c := validConfig(t)
	out, err := RdpgwYAML(c)
	if err != nil {
		t.Fatalf("RdpgwYAML: %v", err)
	}
	for _, want := range []string{
		"Tls: disable",
		"GatewayAddress: pc.example-tailnet.ts.net",
		"ProviderUrl: https://pc.example-tailnet.ts.net/auth/",
		"TokenAuth: true",
		"IdleTimeout: 0",
		"VerifyClientIp: false",
		"    - pc:3389",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered yaml missing %q\n---\n%s", want, out)
		}
	}
}

func TestRdpgwYAMLRefusesIncompleteConfig(t *testing.T) {
	cases := map[string]func(*Config){
		"no hostname":    func(c *Config) { c.Hostname = "" },
		"no targets":     func(c *Config) { c.Targets = nil },
		"no client id":   func(c *Config) { c.ClientID = "" },
		"no secret":      func(c *Config) { c.ClientSecret = "" },
		"bad key length": func(c *Config) { c.SessionKey = "short" },
	}
	for name, mutate := range cases {
		c := validConfig(t)
		mutate(&c)
		if _, err := RdpgwYAML(c); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// These configs are read by Windows programs, so paths must use backslashes
// no matter which platform rendered them.
func TestRenderedPathsAreWindowsStyle(t *testing.T) {
	c := validConfig(t)
	yaml, err := RdpgwYAML(c)
	if err != nil {
		t.Fatalf("RdpgwYAML: %v", err)
	}
	want := `Defaults: C:\ProgramData\RDGateway\rdpgw\default.rdp`
	if !strings.Contains(yaml, want) {
		t.Errorf("rdpgw.yaml should contain %q\n---\n%s", want, yaml)
	}
	ini, err := GiteaAppIni(c, "secret", "")
	if err != nil {
		t.Fatalf("GiteaAppIni: %v", err)
	}
	if strings.Contains(ini, "RDGateway/") {
		t.Errorf("app.ini contains a forward-slash path\n---\n%s", ini)
	}
}

func TestWinJoinNormalises(t *testing.T) {
	cases := map[string]string{
		winJoin(`C:\root\`, "a", "b"): `C:\root\a\b`,
		winJoin("C:/root", "a"):       `C:\root\a`,
		winJoin("C:/root", "", "a"):   `C:\root\a`,
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("winJoin = %q, want %q", got, want)
		}
	}
}

func TestGiteaAppIniEnforcesTwoFactor(t *testing.T) {
	c := validConfig(t)
	out, err := GiteaAppIni(c, "secret", "token")
	if err != nil {
		t.Fatalf("GiteaAppIni: %v", err)
	}
	for _, want := range []string{
		"TWO_FACTOR_AUTH    = enforced",
		"DISABLE_REGISTRATION              = true",
		"REQUIRE_SIGNIN_VIEW               = true",
		"DISABLE_HTTP_GIT = true",
		"HTTP_ADDR    = 127.0.0.1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("app.ini missing %q\n---\n%s", want, out)
		}
	}
}

// Gitea's ROOT_URL does take a trailing slash, unlike rdpgw's issuer.
func TestGiteaRootURLMatchesIssuer(t *testing.T) {
	c := validConfig(t)
	out, _ := GiteaAppIni(c, "secret", "token")
	if !strings.Contains(out, "ROOT_URL     = "+c.GiteaRootURL()) {
		t.Errorf("ROOT_URL should be the issuer plus a slash\n---\n%s", out)
	}
}

func TestDefaultRDPUsesCRLF(t *testing.T) {
	out := DefaultRDP()
	if !strings.Contains(out, "\r\n") {
		t.Error("the .rdp template should use CRLF line endings")
	}
	// multimon only works in fullscreen; shipping one without the other is a
	// silent no-op.
	if strings.Contains(out, "use multimon:i:1") && !strings.Contains(out, "screen mode id:i:2") {
		t.Error("use multimon requires screen mode id:i:2")
	}
	// connection type is ignored unless autodetect is explicitly disabled.
	if strings.Contains(out, "connection type:i:6") && !strings.Contains(out, "networkautodetect:i:0") {
		t.Error("connection type requires networkautodetect:i:0 to take effect")
	}
}

func TestClientINI(t *testing.T) {
	out := ClientINI("pc.tailnet.ts.net", `\.\max`, map[string]string{"MacBook-Pro": `\.\remotr`})
	for _, want := range []string{
		"Hostname=pc.tailnet.ts.net",
		`DefaultUser=\.\max`,
		"[UserRules]",
		`macbook-pro=\.\remotr`, // lowercased, because the AHK matches lowercase
	} {
		if !strings.Contains(out, want) {
			t.Errorf("client ini missing %q\n---\n%s", want, out)
		}
	}
}

func TestClientINIOmitsEmptyUserRules(t *testing.T) {
	out := ClientINI("pc.tailnet.ts.net", `\.\max`, nil)
	if strings.Contains(out, "[UserRules]") {
		t.Error("an empty UserRules section should be omitted entirely")
	}
}

func TestNewPasswordRejectsShortLengths(t *testing.T) {
	if _, err := NewPassword(8); err == nil {
		t.Error("expected short password lengths to be refused")
	}
}

// Regression: the rejection-sampling limit was computed as a byte, which
// overflows to 0 for any alphabet whose length divides 256 exactly. Every
// draw was then rejected and generation hung forever rather than failing.
// 2, 4, 16 and 128 all trigger it; 56 (the password alphabet) does not.
func TestRandomStringTerminatesForPowerOfTwoAlphabets(t *testing.T) {
	for _, alphabet := range []string{"01", "0123", hexAlphabet, strings.Repeat("ab", 64)} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if _, err := randomString(64, alphabet); err != nil {
				t.Errorf("alphabet of %d: %v", len(alphabet), err)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("randomString hung for an alphabet of %d characters", len(alphabet))
		}
	}
}

func TestRandomStringUsesOnlyAlphabet(t *testing.T) {
	s, err := randomString(500, hexAlphabet)
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	if len(s) != 500 {
		t.Fatalf("got %d chars, want 500", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(hexAlphabet, r) {
			t.Fatalf("character %q is outside the alphabet", r)
		}
	}
}

// The two steps that issue a code must go through the gate, before Gitea.
// Dropping either line would let an account without two-factor sign in.
func TestCaddyRoutesOAuthCodeStepsThroughGate(t *testing.T) {
	out, err := Caddyfile(validConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	gated := strings.Index(out, "@gated path /login/oauth/authorize /login/oauth/grant")
	gate := strings.Index(out, "forward_auth @gated 127.0.0.1:8082")
	gitea := strings.Index(out, "reverse_proxy 127.0.0.1:3000")
	if gated < 0 || gate < 0 || gitea < 0 {
		t.Fatalf("gate route missing:\n%s", out)
	}
	if !(gated < gate && gate < gitea) {
		t.Fatalf("gate must come before Gitea in the route:\n%s", out)
	}
}

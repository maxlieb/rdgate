package provision

import (
	"errors"
	"strings"
)

// winJoin builds Windows paths regardless of the host we are compiled on.
//
// filepath.Join follows the build platform, so rendering on Linux during
// development produced "C:\ProgramData\RDGateway/rdpgw/default.rdp" - valid
// enough for Windows to open, but it means tests on Linux were checking
// different bytes than ship. These configs are always consumed by Windows
// programs, so the separator is a property of the output, not of the builder.
func winJoin(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, `\`)
}

// The three config files are rendered by pure functions so they can be unit
// tested and shown to the user for review before anything touches disk.

// RdpgwYAML renders rdpgw's configuration.
func RdpgwYAML(c Config) (string, error) {
	if err := ValidateKeys(c); err != nil {
		return "", err
	}
	if c.PublicHost() == "" {
		return "", errors.New("the public address is empty; sign in to Tailscale or enter a domain first")
	}
	if len(c.Targets) == 0 {
		return "", errors.New("no targets configured; rdpgw would refuse every connection")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", errors.New("OIDC client credentials are empty; create the Gitea application first")
	}

	var hosts strings.Builder
	for _, t := range c.Targets {
		hosts.WriteString("    - " + t + "\n")
	}

	var b strings.Builder
	b.WriteString("Server:\n")
	b.WriteString("  Authentication:\n")
	b.WriteString("    - openid\n")
	b.WriteString("  # Funnel terminates TLS and forwards raw TCP, so rdpgw speaks plain HTTP.\n")
	b.WriteString("  Tls: disable\n")
	b.WriteString("  GatewayAddress: " + c.PublicHost() + "\n")
	b.WriteString("  Port: " + itoa(c.RdpgwPort) + "\n")
	b.WriteString("  Hosts:\n")
	b.WriteString(hosts.String())
	b.WriteString("  HostSelection: unsigned\n")
	b.WriteString("  SessionKey: " + c.SessionKey + "\n")
	b.WriteString("  SessionEncryptionKey: " + c.SessionEncryptionKey + "\n")
	b.WriteString("\n")
	b.WriteString("OpenId:\n")
	b.WriteString("  # No trailing slash: rdpgw compares this to the discovery document's\n")
	b.WriteString("  # issuer as an exact string, and a slash fails the match.\n")
	// Must equal the issuer Gitea advertises, which is its ROOT_URL with the
	// trailing slash. rdpgw compares the two as exact strings.
	b.WriteString("  ProviderUrl: " + c.GiteaRootURL() + "\n")
	b.WriteString("  ClientId: " + c.ClientID + "\n")
	b.WriteString("  ClientSecret: '" + c.ClientSecret + "'\n")
	b.WriteString("\n")
	b.WriteString("Caps:\n")
	b.WriteString("  TokenAuth: true\n")
	b.WriteString("  # 0 = never. The capability is negotiated when the session is\n")
	b.WriteString("  # established, so changing it requires a freshly downloaded .rdp.\n")
	b.WriteString("  IdleTimeout: 0\n")
	b.WriteString("\n")
	b.WriteString("Client:\n")
	b.WriteString("  Defaults: " + winJoin(c.InstallRoot, "rdpgw", "default.rdp") + "\n")
	b.WriteString("  SplitUserDomain: false\n")
	b.WriteString("\n")
	b.WriteString("Security:\n")
	b.WriteString("  PAATokenSigningKey: " + c.PAATokenSigningKey + "\n")
	b.WriteString("  PAATokenEncryptionKey: " + c.PAATokenEncKey + "\n")
	b.WriteString("  # Funnel relays traffic, so the address rdpgw sees is not the browser's.\n")
	b.WriteString("  VerifyClientIp: false\n")
	return b.String(), nil
}

// DefaultRDP is the template rdpgw merges into every generated .rdp.
//
// Only lines whose value differs from rdpgw's internal default survive
// serialisation, so listing a setting that matches the default accomplishes
// nothing - it is dropped and mstsc then applies its own default, which is
// sometimes the opposite.
func DefaultRDP() string {
	return strings.Join([]string{
		"audiocapturemode:i:1",
		"camerastoredirect:s:*",
		"connection type:i:6",
		// connection type is ignored unless autodetect is explicitly off.
		"networkautodetect:i:0",
		"bandwidthautodetect:i:0",
		"compression:i:0",
		"videoplaybackmode:i:1",
		"audiomode:i:0",
		"redirectclipboard:i:1",
		"dynamic resolution:i:1",
		// use multimon requires fullscreen (screen mode id 2).
		"use multimon:i:1",
		"screen mode id:i:2",
		"allow font smoothing:i:1",
		"autoreconnection enabled:i:1",
	}, "\r\n") + "\r\n"
}

// GiteaAppIni renders Gitea's configuration, locked down to being a login page
// that happens to be a git forge: no registration, no anonymous access, no git
// over HTTP, no SSH, and TOTP enrollment enforced before the user can proceed.
func GiteaAppIni(c Config, secretKey, internalToken string) (string, error) {
	if c.PublicHost() == "" {
		return "", errors.New("the public address is empty")
	}
	giteaDir := winJoin(c.InstallRoot, "gitea")

	var b strings.Builder
	b.WriteString("APP_NAME = RD Gateway Sign-in\n")
	b.WriteString("RUN_MODE = prod\n")
	b.WriteString("WORK_PATH = " + giteaDir + "\n\n")

	b.WriteString("[server]\n")
	b.WriteString("PROTOCOL     = http\n")
	b.WriteString("HTTP_ADDR    = 127.0.0.1\n")
	b.WriteString("HTTP_PORT    = " + itoa(c.GiteaPort) + "\n")
	b.WriteString("ROOT_URL     = " + c.GiteaRootURL() + "\n")
	b.WriteString("DOMAIN       = " + c.PublicHost() + "\n")
	b.WriteString("DISABLE_SSH  = true\n")
	b.WriteString("OFFLINE_MODE = true\n\n")

	// Gitea refuses to start its database without git, and the one it needs is
	// the copy the installer put under the install root.
	b.WriteString("[git]\n")
	b.WriteString("PATH = " + winJoin(c.InstallRoot, "git", "cmd", "git.exe") + "\n\n")

	b.WriteString("[database]\n")
	b.WriteString("DB_TYPE = sqlite3\n")
	b.WriteString("PATH    = " + winJoin(giteaDir, "data", "gitea.db") + "\n\n")

	b.WriteString("[security]\n")
	b.WriteString("INSTALL_LOCK       = true\n")
	b.WriteString("SECRET_KEY         = " + secretKey + "\n")
	if internalToken != "" {
		b.WriteString("INTERNAL_TOKEN     = " + internalToken + "\n")
	}
	b.WriteString("PASSWORD_HASH_ALGO = pbkdf2\n")
	b.WriteString("# Blocks the user until TOTP is enrolled. Without this the gateway is\n")
	b.WriteString("# internet-facing behind one password for as long as they put it off.\n")
	b.WriteString("TWO_FACTOR_AUTH    = enforced\n\n")

	b.WriteString("[service]\n")
	b.WriteString("DISABLE_REGISTRATION              = true\n")
	b.WriteString("REQUIRE_SIGNIN_VIEW               = true\n")
	b.WriteString("ENABLE_NOTIFY_MAIL                = false\n")
	b.WriteString("DEFAULT_KEEP_EMAIL_PRIVATE        = true\n")
	b.WriteString("DEFAULT_ALLOW_CREATE_ORGANIZATION = false\n")
	b.WriteString("ENABLE_BASIC_AUTHENTICATION       = false\n\n")

	b.WriteString("[repository]\n")
	b.WriteString("DISABLE_HTTP_GIT = true\n\n")

	b.WriteString("[oauth2]\n")
	b.WriteString("ENABLED = true\n\n")

	b.WriteString("[openid]\n")
	b.WriteString("ENABLE_OPENID_SIGNIN = false\n")
	b.WriteString("ENABLE_OPENID_SIGNUP = false\n\n")

	b.WriteString("[log]\n")
	b.WriteString("MODE      = file\n")
	b.WriteString("LEVEL     = Info\n")
	b.WriteString("ROOT_PATH = " + winJoin(giteaDir, "log") + "\n")
	return b.String(), nil
}

// ClientINI renders the config that sits beside RDGHelper.ahk on the client.
// Nothing secret goes in it - usernames only.
func ClientINI(hostname, defaultUser string, userRules map[string]string) string {
	var b strings.Builder
	b.WriteString("; RDGHelper.ini - generated by RDGate\n")
	b.WriteString("; Keep this file beside RDGHelper.ahk on the CLIENT machine.\n\n")
	b.WriteString("[Gateway]\n")
	b.WriteString("Hostname=" + hostname + "\n\n")
	b.WriteString("[Credentials]\n")
	b.WriteString("; The leading \\.\\ forces the client to resolve the account locally.\n")
	b.WriteString("; Without it an Entra-joined machine expands a bare name to a UPN and fails.\n")
	b.WriteString("DefaultUser=" + defaultUser + "\n")
	if len(userRules) > 0 {
		b.WriteString("\n[UserRules]\n")
		b.WriteString("; <lowercase host-name prefix>=<username>\n")
		for prefix, user := range userRules {
			b.WriteString(strings.ToLower(prefix) + "=" + user + "\n")
		}
	}
	return b.String()
}

package provision

import (
	"context"
	"errors"
	"strings"
)

// Windows Firewall blocks inbound connections by default, so a listener that
// is correct in every other respect still looks dead from outside. These rules
// are named, so uninstall can remove exactly what install added.

const (
	FirewallRuleDirect = "RD Gateway (HTTPS)"
	FirewallRuleRelay  = "RD Gateway (relay over Tailscale)"
)

func netsh(ctx context.Context, args ...string) error {
	out, err := Command(ctx, "netsh", append([]string{"advfirewall", "firewall"}, args...)...).CombinedOutput()
	if err != nil {
		return errors.New("netsh: " + strings.TrimSpace(string(out)))
	}
	return nil
}

// AllowDirect opens 443 (and 80, which Let's Encrypt can use) to caddy.exe
// only - scoped to the program rather than to the port, so nothing else that
// later binds 443 inherits an open door.
func AllowDirect(ctx context.Context, caddyExe string) error {
	_ = netsh(ctx, "delete", "rule", "name="+FirewallRuleDirect)
	return netsh(ctx, "add", "rule", "name="+FirewallRuleDirect,
		"dir=in", "action=allow", "protocol=TCP", "localport=80,443",
		"program="+caddyExe, "profile=any")
}

// AllowRelay admits the relay's traffic, and only from the tailnet's address
// range. The listener is already bound to the tailnet address; this makes the
// firewall agree rather than rely on one layer.
func AllowRelay(ctx context.Context, caddyExe string, port int) error {
	_ = netsh(ctx, "delete", "rule", "name="+FirewallRuleRelay)
	return netsh(ctx, "add", "rule", "name="+FirewallRuleRelay,
		"dir=in", "action=allow", "protocol=TCP", "localport="+itoa(port),
		"remoteip=100.64.0.0/10", "program="+caddyExe, "profile=any")
}

func RemoveFirewallRules(ctx context.Context) {
	_ = netsh(ctx, "delete", "rule", "name="+FirewallRuleDirect)
	_ = netsh(ctx, "delete", "rule", "name="+FirewallRuleRelay)
}

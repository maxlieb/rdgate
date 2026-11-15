package provision

import (
	"errors"
	"fmt"
	"strings"
)

// The relay is a small public server that owns the domain and the certificate
// and forwards everything to this machine over Tailscale. It is the one front
// door that needs a second machine, which is why the app generates its setup
// script instead of describing it.

// RelayUpstream is where the relay sends traffic: this machine, on the
// tailnet, on the home Caddy's port.
func (c Config) RelayUpstream() string {
	return fmt.Sprintf("%s:%d", c.TailscaleIP, c.CaddyPort)
}

// VPSCaddyfile is what runs on the server.
func VPSCaddyfile(c Config) (string, error) {
	domain, err := NormalizeHost(c.PublicDomain)
	if err != nil {
		return "", err
	}
	if _, err := validateIPv4(c.TailscaleIP); err != nil {
		return "", errors.New("this machine has no Tailscale address yet")
	}
	if err := validateEmail(c.ACMEEmail); err != nil {
		return "", err
	}
	var b strings.Builder
	if c.ACMEEmail != "" {
		b.WriteString("{\n\temail " + c.ACMEEmail + "\n}\n\n")
	}
	b.WriteString(domain + " {\n")
	b.WriteString("\treverse_proxy " + c.RelayUpstream() + " {\n")
	// RD Gateway holds one response open for the whole session. Forward it as
	// it arrives, and do not time it out.
	b.WriteString("\t\tflush_interval -1\n")
	b.WriteString("\t\ttransport http {\n\t\t\tresponse_header_timeout 0\n\t\t\tdial_timeout 10s\n\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// VPSScript returns a bash script for an Ubuntu or Debian server. It is safe to
// run twice. Every value interpolated into it has been validated, because the
// person will paste this into a root shell.
func VPSScript(c Config) (string, error) {
	caddyfile, err := VPSCaddyfile(c)
	if err != nil {
		return "", err
	}
	domain, _ := NormalizeHost(c.PublicDomain)
	ip, _ := validateIPv4(c.TailscaleIP)

	var b strings.Builder
	b.WriteString(vpsHead)
	b.WriteString("DOMAIN='" + domain + "'\n")
	b.WriteString("UPSTREAM='" + ip + ":" + itoa(c.CaddyPort) + "'\n\n")
	b.WriteString("CADDYFILE=$(cat <<'CADDYEOF'\n" + caddyfile + "CADDYEOF\n)\n")
	b.WriteString(vpsBody)
	return b.String(), nil
}

const vpsHead = `#!/usr/bin/env bash
# Relay setup for RDGate.
#
# Makes this server the public face of your gateway: it holds the domain and
# the certificate, and passes traffic to your PC over Tailscale. Safe to run
# again. Run it on the server as a user that can use sudo.
set -euo pipefail

say()  { printf '\n==> %s\n' "$*"; }
warn() { printf '\n!! %s\n' "$*" >&2; }
SUDO=''; [ "$(id -u)" -eq 0 ] || SUDO='sudo'

`

const vpsBody = `
say "1/5  Tailscale"
if ! command -v tailscale >/dev/null 2>&1; then
  curl -fsSL https://tailscale.com/install.sh | $SUDO sh
fi
if ! $SUDO tailscale status >/dev/null 2>&1; then
  echo "Open the link below and sign in with the SAME Tailscale account as your PC."
  $SUDO tailscale up --hostname=rdgw-relay --accept-dns=false
fi

say "2/5  Can this server reach your PC over Tailscale?"
reached=''
for i in 1 2 3 4 5 6; do
  if curl -s -o /dev/null --max-time 5 "http://$UPSTREAM/"; then reached=yes; break; fi
  sleep 5
done
if [ -z "$reached" ]; then
  warn "Could not reach $UPSTREAM. Check that your PC is on, signed in to the same"
  warn "Tailscale account, and that the RDGate shows 'Web front end' as running."
  warn "Continuing anyway; the relay will start working as soon as the PC is reachable."
fi

say "3/5  Caddy (the web server that gets the certificate)"
if ! command -v caddy >/dev/null 2>&1; then
  $SUDO apt-get update -y
  $SUDO apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | \
    $SUDO gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | \
    $SUDO tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
  $SUDO apt-get update -y
  $SUDO apt-get install -y caddy
fi
printf '%s\n' "$CADDYFILE" | $SUDO tee /etc/caddy/Caddyfile >/dev/null
$SUDO caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
$SUDO systemctl enable --now caddy
$SUDO systemctl reload caddy || $SUDO systemctl restart caddy

say "4/5  Opening ports 80 and 443 on this server"
# Oracle's Ubuntu images ship iptables rules that reject everything but SSH,
# so the cloud console rule alone is not enough.
if command -v iptables >/dev/null 2>&1; then
  for p in 80 443; do
    $SUDO iptables -C INPUT -p tcp --dport $p -j ACCEPT 2>/dev/null || \
      $SUDO iptables -I INPUT 1 -p tcp --dport $p -j ACCEPT
  done
  if command -v netfilter-persistent >/dev/null 2>&1; then
    $SUDO netfilter-persistent save >/dev/null
  else
    $SUDO DEBIAN_FRONTEND=noninteractive apt-get install -y iptables-persistent >/dev/null
    $SUDO netfilter-persistent save >/dev/null
  fi
fi
if command -v ufw >/dev/null 2>&1 && $SUDO ufw status | grep -q 'Status: active'; then
  $SUDO ufw allow 80/tcp; $SUDO ufw allow 443/tcp
fi

say "5/5  Checking"
myip=$(curl -s --max-time 8 https://api.ipify.org || true)
dnsip=$(getent hosts "$DOMAIN" | awk '{print $1; exit}' || true)
echo "This server's address : ${myip:-unknown}"
echo "$DOMAIN points to      : ${dnsip:-nothing yet}"
if [ -n "$myip" ] && [ "$myip" != "$dnsip" ]; then
  warn "The domain does not point at this server yet. Add an A record for"
  warn "$DOMAIN -> $myip at your DNS provider. Certificates cannot be issued until it does."
fi
echo
echo "Done. In the RDGate, press 'Test the relay'."
echo "Certificates can take a minute to appear the first time."
`

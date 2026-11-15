package provision

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Two addresses settle whether anything from the internet can reach this PC
// directly. The router's own WAN address (the one it got from the provider)
// and the address the internet sees for this connection. When they differ, or
// the WAN address is private or in 100.64.0.0/10, the provider is translating
// again upstream (CGNAT or double NAT). Port forwarding cannot fix that.

// Verdicts from ClassifyWAN.
const (
	VerdictDirect   = "direct"   // public WAN address, matching the internet's view
	VerdictCGNAT    = "cgnat"    // WAN address is private or shared (100.64.0.0/10)
	VerdictMismatch = "mismatch" // WAN address is public but differs from the internet's
	VerdictUnsure   = "unsure"   // the internet's view could not be read
)

// ClassifyWAN compares the router's WAN address with the public address.
// public may be empty when the outside check failed; then only the WAN
// address can be judged, and a public one is reported as unconfirmed.
func ClassifyWAN(wan, public string) (verdict, reason string) {
	if !IsPublicIPv4(wan) {
		return VerdictCGNAT, "The router's address (" + wan + ") is not on the internet. Your provider " +
			"puts you behind carrier-grade NAT (CGNAT), so nothing outside can reach this PC directly, " +
			"and forwarding a port will not help. Use Tailscale Funnel or a server of your own instead."
	}
	if public == "" {
		return VerdictUnsure, "The router's address (" + wan + ") is public, but the internet's view of " +
			"this connection could not be read, so it is not confirmed."
	}
	if public != wan {
		return VerdictMismatch, "The router's address (" + wan + ") differs from the address the internet " +
			"sees (" + public + "). Something upstream translates addresses too, so forwarding a port will " +
			"not reach this PC. Use Tailscale Funnel or a server of your own instead."
	}
	return VerdictDirect, "The router's address (" + wan + ") is public and matches the address the internet " +
		"sees (" + public + "). Forwarding port 443 to this PC should work."
}

// PublicIPv4 asks an outside service what address this connection shows. It
// returns only an address, nothing about the person.
func PublicIPv4(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for _, u := range []string{"https://api.ipify.org", "https://icanhazip.com"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusOK && net.ParseIP(ip) != nil {
			return ip, nil
		}
	}
	return "", errors.New("could not reach an outside address service")
}

// CheckWAN judges an address the person copied from the router's status page.
func CheckWAN(ctx context.Context, wan string) (DirectCheck, error) {
	wan = strings.TrimSpace(wan)
	if net.ParseIP(wan) == nil || net.ParseIP(wan).To4() == nil {
		return DirectCheck{}, errors.New("\"" + wan + "\" is not an IPv4 address; copy the WAN or internet address from the router's status page")
	}
	public, _ := PublicIPv4(ctx)
	verdict, reason := ClassifyWAN(wan, public)
	return DirectCheck{
		ExternalIP: wan, PublicIP: public, Verdict: verdict,
		Public: verdict == VerdictDirect, CGNAT: verdict == VerdictCGNAT || verdict == VerdictMismatch,
		Reason: reason,
	}, nil
}

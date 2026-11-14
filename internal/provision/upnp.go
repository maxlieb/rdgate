package provision

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UPnP is how direct mode avoids asking anyone to log into a router. Most home
// routers speak it, and where they do, one request opens the port. Where they
// do not, the wizard falls back to showing the two settings to change.
//
// This is the Internet Gateway Device protocol, written against the standard
// library only: discovery over SSDP, then SOAP to the router's control URL.

// IGD is a discovered router.
type IGD struct {
	ControlURL string
	Service    string // full service type, needed for the SOAPAction header
	LocalIP    string // this machine's address on the router's network
}

const (
	ssdpAddr = "239.255.255.250:1900"
	ssdpMsg  = "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 2\r\n" +
		"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n\r\n"
)

// DiscoverIGD looks for a router that will accept port mappings.
func DiscoverIGD(ctx context.Context) (*IGD, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	dst, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(4 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if _, err := conn.WriteTo([]byte(ssdpMsg), dst); err != nil {
		return nil, err
	}

	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return nil, errors.New("no router answered; UPnP is probably switched off on it")
		}
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if len(line) > 9 && strings.EqualFold(line[:9], "location:") {
				if igd, err := igdFromLocation(ctx, strings.TrimSpace(line[9:])); err == nil {
					return igd, nil
				}
			}
		}
	}
}

type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type upnpDevice struct {
	Services []upnpService `xml:"serviceList>service"`
	Devices  []upnpDevice  `xml:"deviceList>device"`
}

type upnpRoot struct {
	URLBase string     `xml:"URLBase"`
	Device  upnpDevice `xml:"device"`
}

func findWANService(d upnpDevice) (upnpService, bool) {
	for _, s := range d.Services {
		if strings.Contains(s.ServiceType, "WANIPConnection") ||
			strings.Contains(s.ServiceType, "WANPPPConnection") {
			return s, true
		}
	}
	for _, c := range d.Devices {
		if s, ok := findWANService(c); ok {
			return s, true
		}
	}
	return upnpService{}, false
}

// igdFromLocation reads a router's device description. Split out from
// discovery so it can be tested against a local server.
func igdFromLocation(ctx context.Context, location string) (*IGD, error) {
	loc, err := url.Parse(location)
	if err != nil || loc.Host == "" {
		return nil, errors.New("bad location")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var root upnpRoot
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&root); err != nil {
		return nil, err
	}
	svc, ok := findWANService(root.Device)
	if !ok {
		return nil, errors.New("router offers no port-mapping service")
	}

	base := loc
	if root.URLBase != "" {
		if b, err := url.Parse(root.URLBase); err == nil && b.Host != "" {
			base = b
		}
	}
	ctl, err := base.Parse(svc.ControlURL)
	if err != nil {
		return nil, err
	}
	// Only ever talk to the device that answered us on the LAN. A description
	// pointing the control URL elsewhere would turn this into a request
	// forwarder.
	if ctl.Hostname() != loc.Hostname() {
		return nil, errors.New("router description points off-device")
	}

	local, err := localAddrToward(loc.Host)
	if err != nil {
		return nil, err
	}
	return &IGD{ControlURL: ctl.String(), Service: svc.ServiceType, LocalIP: local}, nil
}

// localAddrToward finds which of our addresses the router can reach us on. A
// UDP "connect" sends nothing; it just asks the OS to pick a route.
func localAddrToward(hostport string) (string, error) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	c, err := net.Dial("udp4", net.JoinHostPort(host, "9"))
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

func (g *IGD) soap(ctx context.Context, action, args string) (string, error) {
	body := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + ` xmlns:u="` + g.Service + `">` + args +
		`</u:` + action + `></s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.ControlURL, bytes.NewReader([]byte(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+g.Service+"#"+action+`"`)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return string(out), fmt.Errorf("router refused %s (%d)", action, resp.StatusCode)
	}
	return string(out), nil
}

// ExternalIP is the address the router believes it has. It is the cheapest way
// to learn that the connection is behind CGNAT: such an address is not public.
func (g *IGD) ExternalIP(ctx context.Context) (string, error) {
	out, err := g.soap(ctx, "GetExternalIPAddress", "")
	if err != nil {
		return "", err
	}
	const open, closeTag = "<NewExternalIPAddress>", "</NewExternalIPAddress>"
	i, j := strings.Index(out, open), strings.Index(out, closeTag)
	if i < 0 || j < i {
		return "", errors.New("router did not report its address")
	}
	return strings.TrimSpace(out[i+len(open) : j]), nil
}

// AddMapping forwards externalPort to this machine. A lease of zero is
// permanent on most routers but ignored or rejected on some, so callers
// refresh periodically with a finite lease rather than relying on it.
func (g *IGD) AddMapping(ctx context.Context, externalPort, internalPort int, desc string, leaseSeconds int) error {
	args := "<NewRemoteHost></NewRemoteHost>" +
		"<NewExternalPort>" + itoa(externalPort) + "</NewExternalPort>" +
		"<NewProtocol>TCP</NewProtocol>" +
		"<NewInternalPort>" + itoa(internalPort) + "</NewInternalPort>" +
		"<NewInternalClient>" + g.LocalIP + "</NewInternalClient>" +
		"<NewEnabled>1</NewEnabled>" +
		"<NewPortMappingDescription>" + xmlEscape(desc) + "</NewPortMappingDescription>" +
		"<NewLeaseDuration>" + itoa(leaseSeconds) + "</NewLeaseDuration>"
	_, err := g.soap(ctx, "AddPortMapping", args)
	return err
}

func (g *IGD) DeleteMapping(ctx context.Context, externalPort int) error {
	_, err := g.soap(ctx, "DeletePortMapping",
		"<NewRemoteHost></NewRemoteHost><NewExternalPort>"+itoa(externalPort)+
			"</NewExternalPort><NewProtocol>TCP</NewProtocol>")
	return err
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// IsPublicIPv4 reports whether an address could be reached from the internet.
// Private ranges and the 100.64.0.0/10 block carrier-grade NAT uses are not.
func IsPublicIPv4(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return false
	}
	for _, cidr := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8", "0.0.0.0/8",
	} {
		if _, n, _ := net.ParseCIDR(cidr); n.Contains(ip) {
			return false
		}
	}
	return true
}

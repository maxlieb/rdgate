package provision

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// NAT-PMP (RFC 6886) asks the router for its internet address over UDP. Unlike
// the router's web page it needs no login, and many home routers answer it
// even when UPnP is switched off.
const natpmpPort = 5351

// DefaultGateway returns the router's address: the next hop of the default route.
func DefaultGateway(ctx context.Context) (string, error) {
	out, err := Command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | Select-Object -First 1).NextHop").Output()
	if err != nil {
		return "", fmt.Errorf("reading the default route: %w", err)
	}
	ip := strings.TrimSpace(string(out))
	if p := net.ParseIP(ip); p == nil || p.To4() == nil {
		return "", errors.New("no default route to a router was found")
	}
	return ip, nil
}

// NATPMPExternalIP asks the router at hostport (for example 192.168.1.1:5351)
// for the public IPv4 address it has on its internet side.
func NATPMPExternalIP(ctx context.Context, hostport string) (string, error) {
	conn, err := net.Dial("udp", hostport)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)

	// Version 0, opcode 0: "what is your external address?"
	if _, err := conn.Write([]byte{0, 0}); err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	n, err := conn.Read(buf)
	if err != nil {
		return "", errors.New("the router did not answer the address request")
	}
	if n < 12 || buf[0] != 0 || buf[1] != 128 {
		return "", errors.New("the router answered in a form this app does not understand")
	}
	if code := binary.BigEndian.Uint16(buf[2:4]); code != 0 {
		return "", fmt.Errorf("the router refused the address request (code %d)", code)
	}
	return net.IPv4(buf[8], buf[9], buf[10], buf[11]).String(), nil
}

// routerExternalIP tries the router's UPnP service first and NAT-PMP second.
// It returns "" when neither answers, which is not an error: the caller then
// says the address is unknown, rather than sending the person to the router.
func routerExternalIP(ctx context.Context) string {
	if igd, err := DiscoverIGD(ctx); err == nil {
		if ip, err := igd.ExternalIP(ctx); err == nil && ip != "" {
			return ip
		}
	}
	gw, err := DefaultGateway(ctx)
	if err != nil {
		return ""
	}
	ip, err := NATPMPExternalIP(ctx, net.JoinHostPort(gw, fmt.Sprint(natpmpPort)))
	if err != nil {
		return ""
	}
	return ip
}

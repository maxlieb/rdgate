package provision

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

// These checks exist because some of what the user types ends up inside a file
// that something else interprets - a Caddyfile, and above all a shell script
// that is pasted into a root shell on a server. A hostname containing a
// newline or a semicolon must never get that far.

var (
	hostRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,190}\.[A-Za-z]{2,}$`)
)

// NormalizeHost lower-cases and trims, and reports whether the result is a
// plain fully-qualified hostname.
func NormalizeHost(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(h), ".")))
	if h == "" {
		return "", errors.New("the address is empty")
	}
	if !hostRe.MatchString(h) {
		return "", errors.New("\"" + h + "\" is not a valid address; use something like rdgw.example.com")
	}
	return h, nil
}

func validateEmail(e string) error {
	if e == "" {
		return nil // optional
	}
	if !emailRe.MatchString(e) {
		return errors.New("that email address does not look right")
	}
	return nil
}

func validateIPv4(s string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return "", errors.New("\"" + s + "\" is not an IPv4 address")
	}
	return ip.To4().String(), nil
}

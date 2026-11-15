package provision

import (
	"net/url"
	"strings"
	"testing"
)

func TestNoIPURLCarriesTheHostnameAndCredentials(t *testing.T) {
	got, err := NoIPURL("MyHome.ddns.net", "max", "s3cret pass")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "dynupdate.no-ip.com" || u.Path != "/nic/update" {
		t.Fatalf("unexpected address %s", got)
	}
	if u.Query().Get("hostname") != "myhome.ddns.net" {
		t.Fatalf("hostname not carried: %s", got)
	}
	if pw, _ := u.User.Password(); pw != "s3cret pass" || u.User.Username() != "max" {
		t.Fatalf("credentials not carried")
	}
}

func TestNoIPURLRejectsIncompleteInput(t *testing.T) {
	for _, c := range [][3]string{
		{"", "max", "pw"},
		{"myhome", "max", "pw"},
		{"my/home.ddns.net", "max", "pw"},
		{"myhome.ddns.net", "", "pw"},
		{"myhome.ddns.net", "max", ""},
	} {
		if _, err := NoIPURL(c[0], c[1], c[2]); err == nil {
			t.Errorf("accepted %v", c)
		}
	}
}

func TestRedactHidesTheNoIPPassword(t *testing.T) {
	raw, _ := NoIPURL("myhome.ddns.net", "max", "topsecret")
	msg := redact("failed for "+raw+" and topsecret", raw)
	if strings.Contains(msg, "topsecret") {
		t.Fatalf("password leaked: %s", msg)
	}
}

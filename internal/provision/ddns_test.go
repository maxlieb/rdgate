package provision

import (
	"strings"
	"testing"
)

func TestNoIPURLCarriesHostnameAndCredentials(t *testing.T) {
	got, err := NoIPURL("MyHome.ddns.net", "max", "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://max:s3cret-pass@dynupdate.no-ip.com/nic/update", "hostname=myhome.ddns.net"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q", got, want)
		}
	}
}

func TestNoIPURLNeedsAllThree(t *testing.T) {
	if _, err := NoIPURL("", "max", "pw"); err == nil {
		t.Error("empty hostname accepted")
	}
	if _, err := NoIPURL("myhome.ddns.net", "", "pw"); err == nil {
		t.Error("empty username accepted")
	}
	if _, err := NoIPURL("myhome.ddns.net", "max", ""); err == nil {
		t.Error("empty password accepted")
	}
}

func TestRedactHidesNoIPPassword(t *testing.T) {
	raw := "https://max:s3cret-pass@dynupdate.no-ip.com/nic/update?hostname=x.ddns.net"
	got := redact("could not reach s3cret-pass", raw)
	if strings.Contains(got, "s3cret-pass") {
		t.Fatalf("password leaked: %s", got)
	}
}

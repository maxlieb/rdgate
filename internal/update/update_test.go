package update

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.43", "0.1.42", true},
		{"0.1.42", "0.1.42", false},
		{"0.1.9", "0.1.10", false},
		{"0.2.0", "0.1.99", true},
		{"v0.1.43", "0.1.42", true},
		{"1.0", "0.9.9", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionMatchesInstaller(t *testing.T) {
	iss, err := os.ReadFile(filepath.Join("..", "..", "installer", "rdgw-manager.iss"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`#define AppVersion\s+"([^"]+)"`).FindSubmatch(iss)
	if m == nil {
		t.Fatal("installer has no AppVersion")
	}
	if string(m[1]) != Version {
		t.Fatalf("installer AppVersion %s, update.Version %s: bump both together", m[1], Version)
	}
}

func TestLatestPicksTheSetupFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.1.43","body":"notes","assets":[
			{"name":"Source.zip","browser_download_url":"https://github.com/x/source.zip"},
			{"name":"RDGate-Setup-0.1.43.exe","browser_download_url":"https://github.com/x/RDGate-Setup-0.1.43.exe"}]}`))
	}))
	defer srv.Close()
	rel, err := Latest(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.1.43" || rel.SetupURL != "https://github.com/x/RDGate-Setup-0.1.43.exe" || rel.Notes != "notes" {
		t.Fatalf("unexpected release: %+v", rel)
	}
}

func TestLatestWithoutInstallerIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.1.43","assets":[{"name":"notes.txt","browser_download_url":"https://github.com/x/n"}]}`))
	}))
	defer srv.Close()
	if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error when the release has no installer")
	}
}

func TestDownloadRefusesOtherHosts(t *testing.T) {
	if _, err := Download(context.Background(), http.DefaultClient, "http://example.com/x.exe", t.TempDir()); err == nil {
		t.Fatal("expected a refusal for a non-GitHub address")
	}
}

func TestThumbprintOfInstalledCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "RDGate test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rdgate-signing.cer")
	if err := os.WriteFile(path, der, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := InstalledThumbprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != Thumbprint(der) || len(got) != 40 {
		t.Fatalf("thumbprint %q", got)
	}
}

func TestMissingCertificateMeansNoInstall(t *testing.T) {
	if _, err := InstalledThumbprint(filepath.Join(t.TempDir(), "absent.cer")); err == nil {
		t.Fatal("an install without a certificate must not offer to update")
	}
}

func TestInstallScriptQuotesAndNamesTasks(t *testing.T) {
	script := InstallScript(4242, `C:\Temp\O'Brien\RDGate-Setup-0.1.43.exe`, `C:\Program Files\RDGate`,
		`C:\ProgramData\RDGateway\updates\update.log`, []string{"RDGateway-Gate", "RDGateway-Web"})
	for _, want := range []string{
		`Wait-Process -Id 4242`,
		`'C:\Temp\O''Brien\RDGate-Setup-0.1.43.exe'`,
		`'RDGateway-Gate', 'RDGateway-Web'`,
		`'/VERYSILENT'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
}

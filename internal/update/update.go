// Package update finds and installs newer RDGate releases.
//
// The download is made by this program rather than a browser, so Windows does
// not mark the file as coming from the internet and SmartScreen stays quiet.
// Before anything is run, the installer's Authenticode signature must match the
// certificate that the installer put on this machine on its first install.
// A release that is not signed by that certificate is refused.
package update

import (
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maxlieb/rdgw-manager/internal/provision"
)

// Version is the release this build belongs to. It must match AppVersion in
// installer/rdgw-manager.iss; a test checks that, so a release cannot drift.
const Version = "0.1.49"

// Where releases are published. Overridable in tests.
const latestAPI = "https://api.github.com/repos/maxlieb/rdgate/releases/latest"

// maxSetupBytes bounds a download. The installer is tens of megabytes.
const maxSetupBytes = 300 << 20

var setupName = regexp.MustCompile(`^RDGate-Setup-[0-9.]+\.exe$`)

// Release is the part of a GitHub release the app needs.
type Release struct {
	Version  string // without the leading v
	Notes    string
	SetupURL string
}

// Status is what the Settings panel shows.
type Status struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Newer      bool   `json:"newer"`
	CanInstall bool   `json:"canInstall"`
	Reason     string `json:"reason"`
}

// Latest asks GitHub for the newest published release and picks its setup file.
func Latest(ctx context.Context, client *http.Client, apiURL string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "RDGate/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("unreadable release listing: %w", err)
	}
	rel := Release{Version: strings.TrimPrefix(body.TagName, "v"), Notes: body.Body}
	for _, a := range body.Assets {
		if setupName.MatchString(a.Name) {
			rel.SetupURL = a.URL
		}
	}
	if rel.Version == "" || rel.SetupURL == "" {
		return Release{}, errors.New("the latest release has no installer")
	}
	return rel, nil
}

// Newer reports whether a is a higher version than b. Both are dotted numbers.
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parts(v string) []int {
	var out []int
	for _, p := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// Download fetches the setup file into dir and returns its path.
func Download(ctx context.Context, client *http.Client, url, dir string) (string, error) {
	if !strings.HasPrefix(url, "https://github.com/") {
		return "", fmt.Errorf("refusing to download from %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "RDGate/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download answered %s", resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filepath.Base(url))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxSetupBytes+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(path)
		return "", fmt.Errorf("saving the installer failed: %v %v", copyErr, closeErr)
	}
	if n > maxSetupBytes {
		os.Remove(path)
		return "", errors.New("the installer is larger than expected")
	}
	return path, nil
}

// Thumbprint is the SHA-1 of a DER certificate, the form Windows displays.
func Thumbprint(der []byte) string {
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// InstalledThumbprint reads the certificate the installer placed beside the
// program. It returns an error when there is none, which is the case for
// installs made before signing existed.
func InstalledThumbprint(certPath string) (string, error) {
	der, err := os.ReadFile(certPath)
	if err != nil {
		return "", errors.New("this install has no signing certificate yet; install the signed release once by hand")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", fmt.Errorf("the signing certificate is unreadable: %w", err)
	}
	return Thumbprint(cert.Raw), nil
}

// VerifySignature asks Windows whether file is validly signed by the
// certificate with the given thumbprint.
func VerifySignature(ctx context.Context, file, wantThumbprint string) error {
	script := fmt.Sprintf(
		"$s = Get-AuthenticodeSignature -LiteralPath %s; "+
			"Write-Output $s.Status; Write-Output $s.SignerCertificate.Thumbprint",
		psQuote(file))
	out, err := provision.Command(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("checking the installer's signature failed: %v", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) < 2 {
		return errors.New("the installer's signature could not be read")
	}
	if lines[0] != "Valid" {
		return fmt.Errorf("the installer's signature is %s, so it was not installed", lines[0])
	}
	if !strings.EqualFold(lines[1], wantThumbprint) {
		return errors.New("the installer is signed by a different certificate, so it was not installed")
	}
	return nil
}

// InstallScript returns the PowerShell that runs after RDGate closes. It stops
// the gateway tasks, runs the installer silently, starts again whichever tasks
// were running, and reopens RDGate. Everything it does is written to logPath.
func InstallScript(pid int, installer, appDir, logPath string, tasks []string) string {
	var taskList []string
	for _, t := range tasks {
		taskList = append(taskList, psQuote(t))
	}
	return fmt.Sprintf(`# Written by RDGate %s. Runs after RDGate has closed.
$ErrorActionPreference = 'Continue'
$log = %s
function Log($m) { Add-Content -LiteralPath $log -Value ("{0} {1}" -f (Get-Date -Format s), $m) }
Log 'update started'
Wait-Process -Id %d -ErrorAction SilentlyContinue
$tasks = @(%s)
$running = @()
foreach ($t in $tasks) {
  $state = (schtasks.exe /Query /TN $t /FO CSV /NH 2>$null) -join ' '
  if ($state -match 'Running') { $running += $t }
  schtasks.exe /End /TN $t 2>$null | Out-Null
}
Log ("stopped tasks; was running: " + ($running -join ', '))
Start-Sleep -Seconds 3
$p = Start-Process -FilePath %s -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/CLOSEAPPLICATIONS' -Wait -PassThru
Log ("installer exit code " + $p.ExitCode)
foreach ($t in $running) { schtasks.exe /Run /TN $t 2>$null | Out-Null }
Start-Process -FilePath (Join-Path %s 'rdgate.exe')
Log 'update finished'
Remove-Item -LiteralPath %s -ErrorAction SilentlyContinue
`, Version, psQuote(logPath), pid, strings.Join(taskList, ", "),
		psQuote(installer), psQuote(appDir), psQuote(installer))
}

// psQuote makes a PowerShell single-quoted literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Timeout for the network calls the Settings panel makes.
const Timeout = 20 * time.Second

// NewClient returns the HTTP client used for release checks and downloads.
func NewClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

// LatestAPI is the address that CheckLatest asks.
func LatestAPI() string { return latestAPI }

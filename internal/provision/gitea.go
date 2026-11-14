package provision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Gitea is the OIDC provider. It is deliberately configured as a login page
// that happens to be a git forge: registration off, anonymous access off, git
// over HTTP off, SSH off, and TOTP enrollment enforced.
type Gitea struct {
	Exe      string
	WorkPath string
	AppIni   string
	Port     int
}

// NewGitea derives the paths for a given install root.
func NewGitea(c Config) Gitea {
	dir := winJoin(c.InstallRoot, "gitea")
	return Gitea{
		Exe:      winJoin(dir, "gitea.exe"),
		WorkPath: dir,
		AppIni:   winJoin(dir, "custom", "conf", "app.ini"),
		Port:     c.GiteaPort,
	}
}

// DownloadURL and ChecksumURL point at the official build for this version.
func (g Gitea) DownloadURL(version string) string {
	return fmt.Sprintf("https://dl.gitea.com/gitea/%s/gitea-%s-windows-4.0-amd64.exe", version, version)
}

func (g Gitea) ChecksumURL(version string) string {
	return g.DownloadURL(version) + ".sha256"
}

// EnsureBinary downloads gitea.exe if it is not already present, verifying it
// against the published SHA256.
//
// HTTPS already protects the transfer, so the checksum is not defence against
// a hostile dl.gitea.com - it catches truncated downloads, proxy interference
// and mirror drift. Given this binary ends up running as SYSTEM, cheap
// integrity checking is worth the extra request.
func (g Gitea) EnsureBinary(ctx context.Context, version string, report Reporter) error {
	if fi, err := os.Stat(g.Exe); err == nil && fi.Size() > 0 {
		report.step("Sign-in service", StepSkipped, "already downloaded")
		return nil
	}
	if err := os.MkdirAll(dirOf(g.Exe), 0o700); err != nil {
		return err
	}

	report.step("Sign-in service", StepRunning, "downloading Gitea "+version)
	want, err := fetchChecksum(ctx, g.ChecksumURL(version))
	if err != nil {
		// A missing checksum file should not block installation outright, but
		// the user deserves to know the download went unverified.
		report.step("Sign-in service", StepRunning, "checksum unavailable, continuing unverified")
		want = ""
	}

	tmp := g.Exe + ".part"
	sum, err := downloadTo(ctx, g.DownloadURL(version), tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("downloading Gitea: %w", err)
	}
	if want != "" && !strings.EqualFold(sum, want) {
		_ = os.Remove(tmp)
		return fmt.Errorf("Gitea download failed verification: got %s, expected %s", sum, want)
	}
	if err := os.Rename(tmp, g.Exe); err != nil {
		return err
	}
	report.step("Sign-in service", StepDone, "downloaded and verified")
	return nil
}

func fetchChecksum(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	// Format is "<hex>  <filename>".
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", errors.New("empty checksum file")
	}
	return fields[0], nil
}

func downloadTo(ctx context.Context, url, dest string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteConfig renders and writes app.ini plus the directories Gitea expects.
func (g Gitea) WriteConfig(c Config) error {
	for _, d := range []string{dirOf(g.AppIni), winJoin(g.WorkPath, "data"), winJoin(g.WorkPath, "log")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	secret, err := NewPassword(64)
	if err != nil {
		return err
	}
	internal, err := NewPassword(64)
	if err != nil {
		return err
	}
	ini, err := GiteaAppIni(c, secret, internal)
	if err != nil {
		return err
	}
	return os.WriteFile(g.AppIni, []byte(ini), 0o600)
}

func (g Gitea) cli(ctx context.Context, args ...string) (string, error) {
	args = append(args, "--config", g.AppIni)
	cmd := Command(ctx, g.Exe, args...)
	cmd.Env = append(os.Environ(), "GITEA_WORK_DIR="+g.WorkPath)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Migrate creates Gitea's database tables. Gitea makes them when it starts,
// but the admin commands run before that, against an empty database, and fail
// with "no such table: user". Running migrations first is what the web
// installer would otherwise do. It is safe to run again.
func (g Gitea) Migrate(ctx context.Context) error {
	out, err := g.cli(ctx, "migrate")
	if err != nil {
		return fmt.Errorf("preparing the sign-in database: %s", reason(out))
	}
	return nil
}

// CreateAdmin provisions the single account. Gitea is configured with
// registration disabled, so this is the only way in.
func (g Gitea) CreateAdmin(ctx context.Context, username, password string) error {
	out, err := g.cli(ctx, "admin", "user", "create",
		"--username", username,
		"--password", password,
		"--email", username+"@localhost",
		"--admin", "--must-change-password=false")
	if err != nil {
		// Re-running the installer should not be fatal.
		if strings.Contains(strings.ToLower(out), "already exists") {
			// Setup ran before. Set the password this run reports, so the
			// account and the password shown to the person agree.
			if pout, perr := g.cli(ctx, "admin", "user", "change-password",
				"--username", username, "--password", password,
				"--must-change-password=false"); perr != nil {
				return fmt.Errorf("updating the admin password: %s", reason(pout))
			}
			return nil
		}
		return fmt.Errorf("creating the admin user: %s", reason(out))
	}
	return nil
}

// RemoveSetupTokens deletes the access tokens that earlier setup runs made, so
// they do not pile up on the account. Best effort: a failure here must not stop
// setup, and the token made in this run is not touched.
func (g Gitea) RemoveSetupTokens(ctx context.Context, username, password string) {
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1/users/%s/tokens", g.Port, username)
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var tokens []struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokens) != nil {
		return
	}
	for _, t := range tokens {
		if !strings.HasPrefix(t.Name, "rdgw-setup-") {
			continue
		}
		del, err := http.NewRequestWithContext(ctx, http.MethodDelete,
			base+"/"+url.PathEscape(t.Name), nil)
		if err != nil {
			continue
		}
		del.SetBasicAuth(username, password)
		if dresp, err := client.Do(del); err == nil {
			dresp.Body.Close()
		}
	}
}

// CreateAdminToken mints a token the manager uses to read status.
//
// Scoped to read:admin deliberately: the dashboard only needs to answer "has
// anyone enrolled TOTP", and a token sitting on disk should not also be able
// to change anything.
func (g Gitea) CreateAdminToken(ctx context.Context, username string) (string, error) {
	return g.MintToken(ctx, username, "read:admin")
}

// MintToken creates an access token with the given scopes through the CLI,
// which needs no sign-in. Each token gets its own name; see CreateAdminToken.
func (g Gitea) MintToken(ctx context.Context, username, scopes string) (string, error) {
	// A fixed token name collides with the one from an earlier attempt ("access
	// token name has been used already"), so every run gets its own name.
	token, _, err := g.mintNamed(ctx, username, scopes)
	return token, err
}

// mintNamed is MintToken, and also returns the name so the token can be
// deleted later.
func (g Gitea) mintNamed(ctx context.Context, username, scopes string) (string, string, error) {
	name := "rdgw-setup-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	token, err := g.mintWithName(ctx, username, name, scopes)
	return token, name, err
}

func (g Gitea) mintWithName(ctx context.Context, username, name, scopes string) (string, error) {
	out, err := g.cli(ctx, "admin", "user", "generate-access-token",
		"--username", username, "--token-name", name, "--scopes", scopes, "--raw")
	if err != nil {
		return "", fmt.Errorf("generating an access token: %s", reason(out))
	}
	// --raw prints the bare token, but CLI builds have historically prefixed
	// it with noise, so take the last non-empty line and sanity check it.
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return "", errors.New("gitea returned no token")
	}
	token := lines[len(lines)-1]
	if len(token) < 20 {
		return "", fmt.Errorf("unexpected token output: %q", out)
	}
	return token, nil
}

// CreateOAuthApp registers rdpgw as an OAuth2 client and returns its
// credentials. There is no CLI verb for this, so it goes through the API.
func (g Gitea) CreateOAuthApp(ctx context.Context, token, redirectURI string) (clientID, clientSecret string, err error) {
	body, _ := json.Marshal(map[string]any{
		"name":                "RD Gateway",
		"redirect_uris":       []string{redirectURI},
		"confidential_client": true,
	})
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/user/applications/oauth2", g.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("creating the OAuth application: %s: %s", resp.Status, firstLine(string(raw)))
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("unexpected response creating the OAuth application: %w", err)
	}
	if out.ClientID == "" || out.ClientSecret == "" {
		return "", "", errors.New("Gitea returned empty OAuth credentials")
	}
	return out.ClientID, out.ClientSecret, nil
}

// WaitUntilListening blocks until Gitea accepts connections, so the installer
// can say "waiting for the sign-in service" rather than failing a race.
func (g Gitea) WaitUntilListening(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", g.Port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("the sign-in service did not start listening on %d; check %s",
		g.Port, winJoin(g.WorkPath, "log"))
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '\\'); i > 0 {
		return p[:i]
	}
	return "."
}

// ChangePassword gives the gateway account a new password and returns it. It
// uses Gitea's own command, which needs no API token, so nothing is left
// behind. The caller shows the password once and does not keep it.
func (g Gitea) ChangePassword(ctx context.Context, username string) (string, error) {
	pw, err := NewPassword(20)
	if err != nil {
		return "", err
	}
	out, err := g.cli(ctx, "admin", "user", "change-password",
		"--username", username, "--password", pw, "--must-change-password=false")
	if err != nil {
		return "", fmt.Errorf("changing the password: %s", reason(out))
	}
	return pw, nil
}

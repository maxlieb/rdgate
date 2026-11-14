// Package gate stops accounts without two-factor sign-in from getting an
// OAuth code, and so from getting a token for RD Gateway.
//
// Gitea enforces two-factor on its web and API routes, but not on the OAuth
// routes that rdpgw uses to sign people in. Caddy sends the two authorization
// requests through this gate first. The gate reads the signed-in account's
// security page with the same session cookie and lets the request through
// only when that page shows two-factor is on, or when nobody is signed in and
// Gitea should show its normal login.
//
// The Gitea API was tried first and cannot make this decision: it refuses
// every session, signed in or not, so it cannot tell the two apart.
package gate

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	// SessionCookie is Gitea's session cookie. Only this one is forwarded:
	// the language cookie or any other one must not change what the page says.
	SessionCookie = "i_like_gitea"

	// Markers in the security page. They are link and form targets, not
	// sentences, so they read the same whatever language Gitea shows.
	enrolledMarker    = "two_factor/disable"
	notEnrolledMarker = "two_factor/enroll"
)

// Verdict is whether a request may continue to Gitea.
type Verdict int

const (
	Allow Verdict = iota
	Block
)

// Classify decides from the answer to the security-page request. It fails
// closed: anything it does not recognise is a block.
func Classify(status int, location, body string) (Verdict, string) {
	switch {
	case status >= 300 && status < 400:
		// Gitea sends anonymous visitors to the login page, and a session
		// still waiting for its second factor to the two-factor page. Both
		// are unauthenticated, so Gitea's own flow handles them. Any other
		// redirect (for example a forced password change) is not allowed
		// through on the strength of being redirected.
		if strings.Contains(location, "/user/login") || strings.Contains(location, "/user/two_factor") {
			return Allow, "not signed in"
		}
		return Block, "signed in, but Gitea redirected the account elsewhere"

	case status == http.StatusOK:
		switch {
		case strings.Contains(body, enrolledMarker):
			return Allow, "two-factor is on"
		case strings.Contains(body, notEnrolledMarker):
			return Block, "two-factor is not turned on"
		default:
			return Block, "the security page did not show a two-factor setting"
		}

	default:
		return Block, fmt.Sprintf("Gitea answered %d", status)
	}
}

// Checker asks Gitea about a session.
type Checker struct {
	Gitea  string // base URL, for example http://127.0.0.1:3000
	Client *http.Client
}

// NewChecker returns a checker with a short timeout. A slow Gitea should
// surface as a refused sign-in, not as a hung browser.
func NewChecker(gitea string) Checker {
	return Checker{
		Gitea: strings.TrimRight(gitea, "/"),
		Client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Check reports whether the session may continue. With no session cookie
// the visitor is anonymous, and that is allowed.
func (ck Checker) Check(ctx context.Context, sessionID string) (Verdict, string, error) {
	if sessionID == "" {
		return Allow, "no session", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ck.Gitea+"/user/settings/security", nil)
	if err != nil {
		return Block, "", err
	}
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sessionID})
	req.Header.Set("Accept-Language", "en-US")

	resp, err := ck.Client.Do(req)
	if err != nil {
		return Block, "", err
	}
	defer resp.Body.Close()

	// The page is about 26 KB; a limit guards against a surprise.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil && resp.StatusCode == http.StatusOK {
		return Block, "", err
	}
	v, why := Classify(resp.StatusCode, resp.Header.Get("Location"), string(body))
	return v, why, nil
}

// Handler answers Caddy's forward_auth subrequests. 2xx lets the request
// through; anything else is returned to the browser as it is, so the body
// below is what a blocked visitor sees.
func Handler(ck Checker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		session := ""
		if c, err := r.Cookie(SessionCookie); err == nil {
			session = c.Value
		}
		v, why, err := ck.Check(r.Context(), session)
		if err != nil {
			log.Printf("gate: could not check the session: %v", err)
			writeBlock(w, http.StatusServiceUnavailable, "", "Sign-in is paused",
				"The sign-in service did not answer, so this sign-in cannot be checked. Try again in a minute.")
			return
		}
		if v == Allow {
			log.Printf("gate: allowed (%s)", why)
			w.WriteHeader(http.StatusOK)
			return
		}
		log.Printf("gate: blocked (%s)", why)
		writeBlock(w, http.StatusForbidden, "/auth/user/settings/security/two_factor/enroll",
			"Two-factor sign-in is required",
			"This account does not have two-factor authentication turned on, so it cannot sign in to the gateway. "+
				"Turn it on, then sign in again.")
	})
	return mux
}

func writeBlock(w http.ResponseWriter, status int, link, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	action := ""
	if link != "" {
		action = `<p><a href="` + html.EscapeString(link) + `">Turn on two-factor authentication</a></p>`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;line-height:1.5">
<h1 style="font-size:1.4rem">%s</h1><p>%s</p>%s</body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(text), action)
}

// Serve listens on addr until ctx is cancelled. Loopback only: Caddy is the
// one thing that should reach it.
func Serve(ctx context.Context, addr, gitea string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		return errors.New("the gate must listen on loopback, not " + addr)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           Handler(NewChecker(gitea)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

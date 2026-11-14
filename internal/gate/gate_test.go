package gate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The two fixtures are real Gitea 1.24.3 security pages, one for an account
// with two-factor on and one for an account without it.
func TestClassifyRealPages(t *testing.T) {
	if v, why := Classify(200, "", fixture(t, "security-enrolled.html")); v != Allow {
		t.Fatalf("enrolled account blocked: %s", why)
	}
	if v, why := Classify(200, "", fixture(t, "security-not-enrolled.html")); v != Block {
		t.Fatalf("account without two-factor allowed: %s", why)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
		body     string
		want     Verdict
	}{
		{"anonymous redirected to login", 303, "/auth/user/login?redirect_to=x", "", Allow},
		{"pending second factor", 303, "/auth/user/two_factor", "", Allow},
		{"forced password change is not a pass", 303, "/auth/user/settings/change_password", "", Block},
		{"redirect with no location", 303, "", "", Block},
		{"enrolled", 200, "", "x two_factor/disable y", Allow},
		{"not enrolled", 200, "", "x two_factor/enroll y", Block},
		{"unrecognised page fails closed", 200, "", "<html>something else</html>", Block},
		{"server error fails closed", 500, "", "", Block},
		{"forbidden fails closed", 403, "", "", Block},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v, why := Classify(c.status, c.location, c.body); v != c.want {
				t.Fatalf("got %v (%s), want %v", v, why, c.want)
			}
		})
	}
}

// fakeGitea serves the security page for the cookie value it is given.
func fakeGitea(t *testing.T, pages map[string]func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/settings/security" {
			http.NotFound(w, r)
			return
		}
		c, err := r.Cookie(SessionCookie)
		if err != nil {
			t.Errorf("session cookie was not forwarded")
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.Header.Get("Cookie"), "lang=") {
			t.Errorf("language cookie was forwarded")
		}
		page, ok := pages[c.Value]
		if !ok {
			http.Redirect(w, r, "/user/login", http.StatusSeeOther)
			return
		}
		page(w)
	}))
}

func serve(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { _, _ = w.Write([]byte(body)) }
}

func TestHandlerEndToEnd(t *testing.T) {
	gitea := fakeGitea(t, map[string]func(http.ResponseWriter){
		"enrolled":     serve(fixture(t, "security-enrolled.html")),
		"not-enrolled": serve(fixture(t, "security-not-enrolled.html")),
	})
	defer gitea.Close()
	h := Handler(NewChecker(gitea.URL))

	check := func(cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/check", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	session := func(v string) *http.Cookie { return &http.Cookie{Name: SessionCookie, Value: v} }

	if rec := check(); rec.Code != http.StatusOK {
		t.Errorf("no cookie: got %d, want 200", rec.Code)
	}
	if rec := check(session("unknown")); rec.Code != http.StatusOK {
		t.Errorf("anonymous with stale cookie: got %d, want 200", rec.Code)
	}
	if rec := check(session("enrolled")); rec.Code != http.StatusOK {
		t.Errorf("enrolled: got %d, want 200", rec.Code)
	}
	rec := check(session("not-enrolled"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("not enrolled: got %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "two_factor/enroll") {
		t.Errorf("blocked page does not link to enrolment")
	}
}

func TestLanguageCookieDoesNotChangeTheVerdict(t *testing.T) {
	gitea := fakeGitea(t, map[string]func(http.ResponseWriter){
		"not-enrolled": serve(fixture(t, "security-not-enrolled.html")),
	})
	defer gitea.Close()
	h := Handler(NewChecker(gitea.URL))
	req := httptest.NewRequest(http.MethodGet, "/check", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "he-IL"})
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "not-enrolled"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
}

func TestUnreachableGiteaFailsClosed(t *testing.T) {
	gitea := httptest.NewServer(http.NotFoundHandler())
	url := gitea.URL
	gitea.Close()
	h := Handler(NewChecker(url))
	req := httptest.NewRequest(http.MethodGet, "/check", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "x"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

func TestServeRefusesPublicAddress(t *testing.T) {
	if err := Serve(context.Background(), "0.0.0.0:8082", "http://127.0.0.1:3000"); err == nil {
		t.Fatal("expected refusal to listen on all interfaces")
	}
}

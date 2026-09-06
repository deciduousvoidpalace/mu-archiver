package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mu-dl/internal/httpx"
)

// loginPage mirrors the real mysteriousuniverse.org form (token redacted):
// no action attribute, RememberMe rendered twice, extra unrelated inputs.
const loginPage = `<html><body>
<form id="account" method="post">
<input class="form-control" type="email" id="Input_Email" name="Input.Email" value="" />
<input class="form-control" type="password" id="Input_Password" name="Input.Password" />
<input type="checkbox" checked id="Input_RememberMe" name="Input.RememberMe" value="true" />
<input type="hidden" id="Input_IP" name="Input.IP" value="" />
<input name="__RequestVerificationToken" type="hidden" value="TOK123" />
<input name="Input.RememberMe" type="hidden" value="false" />
<input  type="hidden" name="ip-social" id="ip-social" />
<button type="submit">Log in</button>
</form>
<form id="external-account" method="post" action="/Identity/Account/ExternalLogin?returnUrl=%2F">
<input name="__RequestVerificationToken" type="hidden" value="TOK123" />
<input id="search-text" type="search" placeholder="Enter search text">
</form></body></html>`

func TestBuildValuesRememberMeOrder(t *testing.T) {
	f, err := parseLoginForm(loginPage)
	if err != nil {
		t.Fatal(err)
	}
	if f.action != "" {
		t.Fatalf("action = %q, want empty (posts to self)", f.action)
	}
	v := f.buildValues("me@example.com", "pw")
	if got := v["Input.RememberMe"]; len(got) != 2 || got[0] != "true" || got[1] != "false" {
		t.Fatalf("RememberMe = %v, want [true false]", got)
	}
	if v.Get("Input.Email") != "me@example.com" || v.Get("Input.Password") != "pw" {
		t.Fatalf("credentials not placed: %v", v)
	}
	if v.Get("__RequestVerificationToken") != "TOK123" {
		t.Fatalf("antiforgery token missing: %v", v)
	}
	if _, ok := v["ip-social"]; !ok {
		t.Fatalf("hidden ip-social dropped")
	}
}

func TestLoginFlow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/identity/account/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.SetCookie(w, &http.Cookie{Name: ".AspNetCore.Antiforgery.x", Value: "af", Path: "/"})
			w.Write([]byte(loginPage))
			return
		}
		r.ParseForm()
		if r.PostForm.Get("__RequestVerificationToken") != "TOK123" {
			w.WriteHeader(400)
			return
		}
		if r.PostForm.Get("Input.Email") == "me@example.com" && r.PostForm.Get("Input.Password") == "pw" &&
			r.PostForm["Input.RememberMe"][0] == "true" {
			http.SetCookie(w, &http.Cookie{Name: ".AspNetCore.Identity.Application", Value: "sess", Path: "/"})
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		w.Write([]byte(strings.Replace(loginPage, "<form id=\"account\"", "<div>Invalid login attempt.</div><form id=\"account\"", 1)))
	})
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(".AspNetCore.Identity.Application"); err != nil || c.Value != "sess" {
			http.Redirect(w, r, "/identity/account/login?ReturnUrl=%2Fdashboard", http.StatusFound)
			return
		}
		w.Write([]byte(`<html><a href="/identity/account/logout">Log out</a><input class="rss-id" value="1"/></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := httpx.New(false, 5*time.Second, nil)
	ctx := context.Background()
	if err := Login(ctx, c, srv.URL, "me@example.com", "wrong"); err == nil {
		t.Fatal("expected bad-credentials error")
	}
	if err := Login(ctx, c, srv.URL, "me@example.com", "pw"); err != nil {
		t.Fatalf("login: %v", err)
	}
	fc, _ := httpx.New(true, 5*time.Second, nil)
	fc.ShareJar(c)
	if _, ok := VerifySession(ctx, fc, srv.URL); !ok {
		t.Fatal("session should verify after login")
	}
	empty, _ := httpx.New(true, 5*time.Second, nil)
	if _, ok := VerifySession(ctx, empty, srv.URL); ok {
		t.Fatal("empty session must not verify")
	}
}

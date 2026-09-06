// Package auth performs the ASP.NET Core Identity login used by
// mysteriousuniverse.org. Rather than hard-coding field names, it parses the
// login form to discover the email/password/remember inputs and carries over
// every hidden field (notably __RequestVerificationToken), which makes it
// resilient to markup changes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"mu-dl/internal/httpx"
)

// LoginPath is the default identity login endpoint.
const LoginPath = "/identity/account/login"

// ErrBadCredentials is returned when the site rejects the email/password.
var ErrBadCredentials = errors.New("email or password not accepted")

var (
	formRe  = regexp.MustCompile(`(?is)<form\b[^>]*>.*?</form>`)
	inputRe = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	attrRe  = regexp.MustCompile(`(?is)([a-zA-Z_:-]+)\s*=\s*"([^"]*)"`)
	// markers of an authenticated page
	authedRe = regexp.MustCompile(`(?i)(/identity/account/logout|>\s*log\s*out\s*<|class="rss-id"|id="ClipboardHQ-)`)
	// validation summary text ASP.NET Identity renders on a failed login
	invalidRe = regexp.MustCompile(`(?i)(invalid login attempt|is not a valid|locked out|is invalid)`)
)

type input struct {
	name  string
	value string
	typ   string
}

type form struct {
	action string
	method string
	inputs []input
}

func attrs(tag string) map[string]string {
	m := map[string]string{}
	for _, a := range attrRe.FindAllStringSubmatch(tag, -1) {
		m[strings.ToLower(a[1])] = a[2]
	}
	return m
}

// parseLoginForm finds the <form> that contains a password field and extracts
// its action, method, and inputs.
func parseLoginForm(html string) (*form, error) {
	for _, block := range formRe.FindAllString(html, -1) {
		var f form
		open := block
		if i := strings.Index(block, ">"); i >= 0 {
			open = block[:i]
		}
		fa := attrs(open)
		f.action = fa["action"]
		f.method = strings.ToUpper(fa["method"])
		if f.method == "" {
			f.method = http.MethodPost
		}
		hasPassword := false
		for _, tag := range inputRe.FindAllString(block, -1) {
			a := attrs(tag)
			in := input{name: a["name"], value: a["value"], typ: strings.ToLower(a["type"])}
			if in.name == "" {
				continue
			}
			if in.typ == "password" {
				hasPassword = true
			}
			f.inputs = append(f.inputs, in)
		}
		if hasPassword {
			return &f, nil
		}
	}
	return nil, fmt.Errorf("could not locate a login form with a password field")
}

// buildValues fills in email/password/remember on the discovered form while
// preserving all hidden fields. ASP.NET renders a checkbox plus a hidden
// "false" for RememberMe; the model binder takes the first value, so the
// checkbox value must come first and the hidden duplicate must be *added*,
// not used to overwrite it.
func (f *form) buildValues(email, password string) url.Values {
	v := url.Values{}
	emailSet, passSet := false, false
	for _, in := range f.inputs {
		lname := strings.ToLower(in.name)
		switch {
		case !passSet && in.typ == "password":
			v.Set(in.name, password)
			passSet = true
		case !emailSet && (in.typ == "email" || strings.Contains(lname, "email") || strings.Contains(lname, "username") || strings.Contains(lname, "user")):
			v.Set(in.name, email)
			emailSet = true
		case in.typ == "checkbox" && strings.Contains(lname, "remember"):
			v[in.name] = append([]string{"true"}, v[in.name]...)
		case in.typ == "submit" || in.typ == "button" || in.typ == "search" || in.typ == "radio":
			continue
		default:
			// carry hidden/other fields (antiforgery token, returnUrl, etc.)
			v.Add(in.name, in.value)
		}
	}
	if !passSet {
		v.Set("Input.Password", password)
	}
	if !emailSet {
		v.Set("Input.Email", email)
	}
	return v
}

// Login authenticates against the site using c (which must NOT follow
// redirects, so the 302 that signals success can be observed). On success the
// session cookie is in c's jar.
func Login(ctx context.Context, c *httpx.Client, baseURL, email, password string) error {
	base := strings.TrimRight(baseURL, "/")
	loginURL := base + LoginPath

	// 1. GET the login page to obtain the antiforgery cookie + token.
	page, resp, err := c.GetString(ctx, loginURL)
	if err != nil {
		return fmt.Errorf("fetching login page: %w", err)
	}
	if resp != nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// already logged in? the login page bounces authenticated users home
		return nil
	}
	f, err := parseLoginForm(page)
	if err != nil {
		return err
	}

	// resolve the form action relative to the login URL
	action := loginURL
	if f.action != "" {
		if u, err := url.Parse(loginURL); err == nil {
			if ref, err := u.Parse(f.action); err == nil {
				action = ref.String()
			}
		}
	}

	values := f.buildValues(email, password)

	// 2. POST the credentials.
	req, err := c.NewRequest(ctx, http.MethodPost, action, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", loginURL)
	req.Header.Set("Origin", base)

	resp, err = c.Do(req)
	if err != nil {
		return fmt.Errorf("posting credentials: %w", err)
	}
	defer resp.Body.Close()

	// A 302/303 away from the login page indicates success under ASP.NET
	// Identity. A 200 means the login page was re-rendered with an error.
	switch resp.StatusCode {
	case http.StatusFound, http.StatusSeeOther, http.StatusMovedPermanently, http.StatusTemporaryRedirect:
		loc := strings.ToLower(resp.Header.Get("Location"))
		switch {
		case strings.Contains(loc, "loginwith2fa"):
			return errors.New("this account has two-factor authentication enabled, which the archiver cannot complete; disable 2FA or paste feed URLs instead")
		case strings.Contains(loc, "lockout"):
			return errors.New("account is temporarily locked out after too many failed logins; wait a while before retrying")
		case strings.Contains(loc, "login"):
			return fmt.Errorf("%w (redirected back to %q)", ErrBadCredentials, loc)
		}
		return nil
	case http.StatusOK:
		return ErrBadCredentials
	default:
		return fmt.Errorf("unexpected login response: %s", resp.Status)
	}
}

// VerifySession checks whether the current session is authenticated by
// requesting the dashboard with a redirect-following client and confirming
// we were not bounced to the login form. It returns the dashboard HTML on
// success so callers can parse it without a second request.
func VerifySession(ctx context.Context, c *httpx.Client, baseURL string) (string, bool) {
	dashURL := strings.TrimRight(baseURL, "/") + "/dashboard"
	body, resp, err := c.GetString(ctx, dashURL)
	if err != nil || resp == nil {
		return "", false
	}
	if resp.StatusCode >= 300 {
		return "", false
	}
	if resp.Request != nil && strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return "", false
	}
	if invalidRe.MatchString(body) && !authedRe.MatchString(body) {
		return "", false
	}
	if !authedRe.MatchString(body) {
		return "", false
	}
	return body, true
}

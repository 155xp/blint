package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestClassify(t *testing.T) {
	alt, redir, empty, suspended, foundAlt := 403, 302, "", "Account suspended", 200
	cases := []struct {
		name string
		site Site
		code int
		body string
		want string
	}{
		{"found", Site{ECode: 200, EString: `"id":`, MCode: 404, MString: `"status": "404"`}, 200, `{"id":1}`, "found"},
		{"200 miss text", Site{ECode: 200, EString: `"id":`, MCode: 200, MString: "[]"}, 200, "[]", "not_found"},
		{"ambiguous", Site{ECode: 200, EString: `"id":`, MCode: 404, MString: "missing"}, 200, "nope", "error"},
		{"colon space", Site{ECode: 200, EString: `"profileImageURL":`, MCode: 200, MString: `"user":null`}, 200, `{"user": null}`, "not_found"},
		{"challenge", Site{ECode: 200, EString: `"id":`, MCode: 404, MString: "missing"}, 200, `Just a moment... {"id":1}`, "error"},
		{"403 not found", Site{ECode: 200, EString: `"id":`, MCode: 404, MString: "missing"}, 403, `{"id":1}`, "error"},
		{"alt miss", Site{ECode: 200, EString: "full_name", MCode: 404, MString: "No workspace", MAltCode: &alt, MAltString: strPtr("deactivated due to inactivity")}, 403, "deactivated due to inactivity", "not_found"},
		{"gone", Site{ECode: 200, EString: `"id":`, MCode: 404, MString: "missing"}, 410, "gone", "not_found"},
		{"code only", Site{ECode: 302, EString: "", MCode: 404, MString: "missing"}, 302, "", "found"},
		{"redirect", Site{ECode: 200, EString: "tgme_page_title", MCode: 200, MString: "noindex, nofollow", MAltCode: &redir, MAltString: &empty}, 302, "", "not_found"},
		{"suspended", Site{ECode: 200, EString: `property="og:type" content="profile"`, MCode: 404, MString: "User Profile Not Found", MAltCode: &foundAlt, MAltString: &suspended}, 200, "<h2>Account suspended</h2>", "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.site, tc.code, tc.body); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestCatalogMarkers(t *testing.T) {
	for _, site := range loadSites() {
		if site.MCode == 0 && site.MAltCode == nil {
			t.Errorf("%s has no not-found marker", site.ID)
		}
		if !strings.HasPrefix(site.URICheck, "https://") {
			t.Errorf("%s is not https", site.ID)
		}
	}
}

func TestCheck(t *testing.T) {
	var body string
	var hops int
	mux := http.NewServeMux()
	mux.HandleFunc("/ok/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"id":1}`) })
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") })
	mux.HandleFunc("/gql", func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		body = string(buf)
		if r.Header.Get("Client-ID") != "abc" || r.Header.Get("Cookie") != "" {
			t.Errorf("headers = %v", r.Header)
		}
		fmt.Fprint(w, `{"data":{"user":null}}`)
	})
	mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, "/landed", http.StatusFound)
	})
	mux.HandleFunc("/landed", func(w http.ResponseWriter, r *http.Request) {
		hops++
		fmt.Fprint(w, `{"id":1}`)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = redirectPolicy

	ok := Site{ID: "t", Name: "T", URICheck: srv.URL + "/ok/{account}", ECode: 200, EString: `"id":`, MCode: 404, MString: "missing"}
	if got := check(context.Background(), client, ok, "a.b"); got.Status != "found" || !strings.HasSuffix(got.URL, "/ok/a.b") {
		t.Fatalf("%+v", got)
	}
	ok.StripBadChar = "."
	if got := check(context.Background(), client, ok, "a.b"); !strings.HasSuffix(got.URL, "/ok/ab") {
		t.Fatalf("%+v", got)
	}
	miss := Site{URICheck: srv.URL + "/missing", ECode: 200, EString: `"id":`, MCode: 200, MString: "[]"}
	if got := check(context.Background(), client, miss, "ada"); got.Status != "not_found" {
		t.Fatalf("false hit %+v", got)
	}
	redir, empty := 302, ""
	hop := Site{URICheck: srv.URL + "/go", ECode: 200, EString: `"id":`, MCode: 404, MString: "missing", MAltCode: &redir, MAltString: &empty}
	if got := check(context.Background(), client, hop, "ada"); got.Status != "not_found" || hops != 1 {
		t.Fatalf("%+v hops %d", got, hops)
	}
	pretty := srv.URL + "/u/{account}"
	post := Site{
		URICheck: srv.URL + "/gql", URIPretty: &pretty, Method: "POST",
		Headers: map[string]string{"Client-ID": "abc", "Content-Type": "application/json", "Cookie": "nope"},
		Body:    `{"login":"{account}"}`, ECode: 200, EString: `"profileImageURL":`, MCode: 200, MString: `"user":null`,
	}
	if got := check(context.Background(), client, post, `a"b`); got.Status != "not_found" || body != `{"login":"a\"b"}` {
		t.Fatalf("%+v body %s", got, body)
	}
}

func TestANSIStyles(t *testing.T) {
	r := lipgloss.NewRenderer(os.Stdout)
	r.SetColorProfile(termenv.ANSI)
	v := &view{
		nameW: 6,
		hit:   r.NewStyle().Foreground(lipgloss.Color("2")),
		name:  r.NewStyle().Bold(true).Width(6),
		url:   r.NewStyle().Faint(true),
		miss:  r.NewStyle().Foreground(lipgloss.Color("8")).Faint(true),
		err:   r.NewStyle().Foreground(lipgloss.Color("3")),
		gray:  r.NewStyle().Foreground(lipgloss.Color("8")),
	}
	hit := v.row(Result{Status: "found", Name: "GitHub", URL: "https://github.com/x"})
	if !strings.Contains(hit, "\x1b[32m") || !strings.Contains(hit, "\x1b[1mGitHub") || !strings.Contains(hit, "\x1b[2mhttps://") {
		t.Fatalf("hit %q", hit)
	}
	if strings.Contains(hit, "38;5;") || strings.Contains(hit, "38;2;") {
		t.Fatalf("not ansi 16 %q", hit)
	}
	miss := v.row(Result{Status: "not_found", Name: "GitHub"})
	if !strings.Contains(miss, "90m") || !strings.Contains(miss, "not found") {
		t.Fatalf("miss %q", miss)
	}
	got := v.row(Result{Status: "error", Name: "GitHub", Error: "timeout"})
	if !strings.Contains(got, "\x1b[33m") || !strings.Contains(got, "timeout") {
		t.Fatalf("err %q", got)
	}
	sum := v.summary(report{Found: 2, NotFound: 3, Errors: 1, MS: 900}, "")
	if !strings.Contains(sum, "\x1b[32m2 found") || !strings.Contains(sum, "\x1b[33m1 error") || !strings.Contains(sum, "\x1b[90m") {
		t.Fatalf("summary %q", sum)
	}
}

func strPtr(s string) *string { return &s }

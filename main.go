package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

//go:embed sites.json
var sitesJSON []byte

const (
	workers   = 24
	reqLimit  = 3500 * time.Millisecond
	userAgent = "blint/0.1 (public profile check; +https://github.com/155xp/blint)"
)

type followKey struct{}

type Site struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Category        string            `json:"category"`
	URICheck        string            `json:"uri_check"`
	URIPretty       *string           `json:"uri_pretty"`
	ECode           int               `json:"e_code"`
	EString         string            `json:"e_string"`
	MCode           int               `json:"m_code"`
	MString         string            `json:"m_string"`
	MAltCode        *int              `json:"m_alt_code,omitempty"`
	MAltString      *string           `json:"m_alt_string,omitempty"`
	StripBadChar    string            `json:"strip_bad_char,omitempty"`
	FollowRedirects bool              `json:"follow_redirects,omitempty"`
	Method          string            `json:"method,omitempty"`
	Body            string            `json:"body,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
}

type Result struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Status     string `json:"status"`
	URL        string `json:"url"`
	StatusCode *int   `json:"statusCode"`
	MS         int64  `json:"ms"`
	Error      string `json:"error,omitempty"`
}

type report struct {
	Username string   `json:"username"`
	Found    int      `json:"found"`
	NotFound int      `json:"notFound"`
	Errors   int      `json:"errors"`
	MS       int64    `json:"ms"`
	Results  []Result `json:"results"`
}

type view struct {
	tty, all, asJSON, multi bool
	name, status, url       lipgloss.Style
	dim, bad, muted         lipgloss.Style
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("blint", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	all := fs.Bool("a", false, "show not-found and error results")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, "usage: blint [-a] [--json] <username>[,username...]") }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	users, err := splitUsers(fs.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fs.Usage()
		return 2
	}
	sites := loadSites()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); stop() }()
	fi, statErr := os.Stdout.Stat()
	v := newView(statErr == nil && fi.Mode()&os.ModeCharDevice != 0, *all, *asJSON, sites, len(users) > 1)
	var reports []report
	for _, user := range users {
		if ctx.Err() != nil {
			break
		}
		reports = append(reports, scan(ctx, &http.Client{Timeout: 5 * time.Second, CheckRedirect: redirectPolicy}, sites, user, v))
	}
	if v.asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(reports); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	return 0
}

func splitUsers(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, arg := range args {
		for _, part := range strings.Split(arg, ",") {
			u := strings.TrimSpace(part)
			if u == "" || seen[u] {
				continue
			}
			if len(u) > 80 || strings.ContainsAny(u, " \t\r\n/\\?#{}") {
				return nil, fmt.Errorf("invalid username %q", u)
			}
			for _, r := range u {
				if r < 0x21 || r == 0x7f {
					return nil, fmt.Errorf("invalid username %q", u)
				}
			}
			seen[u] = true
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("username required")
	}
	return out, nil
}

func loadSites() []Site {
	var cat struct {
		Sites []Site `json:"sites"`
	}
	if err := json.Unmarshal(sitesJSON, &cat); err != nil {
		fmt.Fprintln(os.Stderr, "sites:", err)
		os.Exit(2)
	}
	return cat.Sites
}

func redirectPolicy(req *http.Request, via []*http.Request) error {
	if req.Context().Value(followKey{}) == true && len(via) < 5 {
		return nil
	}
	return http.ErrUseLastResponse
}

func scan(ctx context.Context, client *http.Client, sites []Site, user string, v *view) report {
	jobs, out := make(chan Site), make(chan Result)
	var wg sync.WaitGroup
	n := workers
	if n > len(sites) {
		n = len(sites)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for site := range jobs {
				out <- check(ctx, client, site, user)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, site := range sites {
			select {
			case <-ctx.Done():
				return
			case jobs <- site:
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()

	start, done := time.Now(), 0
	rep := report{Username: user, Results: []Result{}}
	p := &progress{total: len(sites), left: len(sites)}
	if v.multi {
		p.label = user
	}
	p.draw(v)
	for res := range out {
		if res.Status == "canceled" {
			continue
		}
		done++
		p.left = len(sites) - done
		switch res.Status {
		case "found":
			rep.Found++
		case "not_found":
			rep.NotFound++
		default:
			rep.Errors++
		}
		if v.all || res.Status == "found" {
			rep.Results = append(rep.Results, res)
		}
		v.emit(p, res)
		p.draw(v)
	}
	p.clear()
	rep.MS = time.Since(start).Milliseconds()
	if v.live() {
		errWord := "errors"
		if rep.Errors == 1 {
			errWord = "error"
		}
		line := fmt.Sprintf("%d found · %d not found · %d %s · %s", rep.Found, rep.NotFound, rep.Errors, errWord, fmtDur(rep.MS))
		if v.multi {
			line = user + " · " + line
		}
		fmt.Println(v.muted.Render(line))
	}
	return rep
}

func fmtDur(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func check(ctx context.Context, client *http.Client, site Site, username string) (res Result) {
	start := time.Now()
	defer func() { res.MS = time.Since(start).Milliseconds() }()
	account := username
	if site.StripBadChar != "" {
		account = strings.ReplaceAll(username, site.StripBadChar, "")
	}
	pretty := site.URICheck
	if site.URIPretty != nil && *site.URIPretty != "" {
		pretty = *site.URIPretty
	}
	res = Result{ID: site.ID, Name: site.Name, Category: site.Category, Status: "error", URL: strings.ReplaceAll(pretty, "{account}", account)}
	checkURL := strings.ReplaceAll(site.URICheck, "{account}", account)
	if account == "" || !strings.HasPrefix(checkURL, "https://") {
		res.Error = "skipped"
		return res
	}
	method, payload := http.MethodGet, ""
	if strings.EqualFold(site.Method, http.MethodPost) {
		method = http.MethodPost
		quoted, _ := json.Marshal(account)
		payload = strings.ReplaceAll(site.Body, "{account}", string(quoted[1:len(quoted)-1]))
	}
	reqCtx, cancel := context.WithTimeout(ctx, reqLimit)
	defer cancel()
	if site.FollowRedirects {
		reqCtx = context.WithValue(reqCtx, followKey{}, true)
	}
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, checkURL, body)
	if err != nil {
		res.Error = "skipped"
		return res
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	for k, val := range site.Headers {
		lk := strings.ToLower(k)
		if val != "" && (lk == "accept" || lk == "client-id" || lk == "content-type") {
			req.Header.Set(k, val)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			res.Status = "canceled"
			return res
		}
		res.Error = "network"
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			res.Error = "timeout"
		}
		return res
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	code := resp.StatusCode
	res.StatusCode = &code
	page := string(buf)
	res.Status = classify(site, code, page)
	if res.Status == "error" {
		res.Error = "unclear"
		if code == 429 {
			res.Error = "rate limited"
		} else if challenged(code, page) {
			res.Error = "blocked"
		}
	}
	return res
}

func classify(site Site, code int, body string) string {
	if !challenged(code, body) && code == site.ECode && has(body, site.EString) {
		return "found"
	}
	alt := false
	if site.MAltCode != nil && code == *site.MAltCode {
		needle := ""
		if site.MAltString != nil {
			needle = *site.MAltString
		}
		alt = has(body, needle)
	}
	if code == 410 || (code == site.MCode && has(body, site.MString)) || alt {
		return "not_found"
	}
	return "error"
}

func has(body, needle string) bool {
	if needle == "" || strings.Contains(body, needle) {
		return true
	}
	return strings.Contains(strings.ReplaceAll(body, ": ", ":"), strings.ReplaceAll(needle, ": ", ":"))
}

func challenged(code int, body string) bool {
	if code == 401 || code == 403 || code == 407 || code == 429 {
		return true
	}
	for _, needle := range []string{
		"Just a moment...", "Client Challenge", "Attention Required! | Cloudflare",
		"Vercel Security Checkpoint", "Making sure you're not a bot!",
		"https://www.google.com/sorry/", "Our systems have detected unusual traffic",
	} {
		if strings.Contains(body, needle) {
			return true
		}
	}
	return false
}

func newView(tty, all, asJSON bool, sites []Site, multi bool) *view {
	v := &view{tty: tty, all: all, asJSON: asJSON, multi: multi}
	if !v.live() {
		return v
	}
	width := 8
	for _, site := range sites {
		if n := len(site.Name); n > width {
			width = n
		}
	}
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	v.name, v.status = fg("245").Width(width), fg("243").Width(10)
	v.url, v.dim, v.bad, v.muted = fg("252"), fg("240"), fg("167"), fg("245")
	return v
}

func (v *view) live() bool { return v.tty && !v.asJSON }

func (v *view) emit(p *progress, res Result) {
	if v.asJSON || (res.Status != "found" && !v.all) {
		return
	}
	if !v.tty {
		if res.Status == "found" {
			fmt.Println(res.URL)
		}
		return
	}
	p.clear()
	name := v.name.Render(res.Name)
	if !v.all {
		fmt.Println(name + "  " + v.url.Render(res.URL))
		return
	}
	label, detail, style := "found", res.URL, v.url
	switch res.Status {
	case "not_found":
		label, detail, style = "not found", "", v.dim
	default:
		if res.Status != "found" {
			label, detail, style = "error", res.Error, v.bad
		}
	}
	fmt.Println(name + "  " + v.status.Render(label) + "  " + style.Render(detail))
}

type progress struct {
	on          bool
	total, left int
	label       string
}

func (p *progress) draw(v *view) {
	if !v.live() {
		return
	}
	msg := fmt.Sprintf("checking %d sites · %d left", p.total, p.left)
	if p.label != "" {
		msg = p.label + " · " + msg
	}
	fmt.Fprint(os.Stdout, "\r\033[2K"+v.muted.Render(msg))
	p.on = true
}

func (p *progress) clear() {
	if p.on {
		fmt.Fprint(os.Stdout, "\r\033[2K")
		p.on = false
	}
}

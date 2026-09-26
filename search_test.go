package search_test

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	search "github.com/Elagoht/collage-search"
	"github.com/Elagoht/collage/pkg/collage"
)

const layout = `<!DOCTYPE html><html lang="{{.Lang}}"><head><title>{{.Title}} — Site</title><meta name="description" content="{{.Description}}">{{if .NoIndex}}<meta name="robots" content="noindex, follow">{{end}}</head>
<body><nav>Home Docs Blog</nav>{{searchBox}}
{{if .Main}}<main>{{end}}<h1>{{.Title}}</h1>{{.Body}}<script>var notText = 1;</script><div data-search-ignore>ignored words</div>{{if .Main}}</main>{{end}}<footer>footer words</footer></body></html>`

type view struct {
	Lang, Title, Description string
	Body                     string
	Main, NoIndex            bool
}

func newSite(opts search.Options, pages map[string]view) (*collage.App, error) {
	app, err := collage.New(&collage.Config{
		Server: collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{
			"t/p.html": {Data: []byte(strings.ReplaceAll(layout, "{{.Body}}", "{{.BodyHTML}}"))},
		}, Root: "t"},
		Locale:  collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}},
		Plugins: []collage.Plugin{search.New(opts)},
	})
	if err != nil {
		return nil, err
	}
	for path, v := range pages {
		locale := "en"
		if strings.HasPrefix(path, "/tr/") {
			locale, path = "tr", strings.TrimPrefix(path, "/tr")
		}
		data := map[string]any{ // any: the template's data, of mixed types
			"Lang": v.Lang, "Title": v.Title, "Description": v.Description,
			"BodyHTML": template.HTML(v.Body), "Main": v.Main, "NoIndex": v.NoIndex,
		}
		name := locale + path
		page := collage.NewPage(name).
			WithContent(collage.NewFragment(name, "p.html").WithData(data).Build()).
			WithPath(locale, path).
			Build()
		if err := app.RegisterPage(page); err != nil {
			return nil, err
		}
	}
	return app, nil
}

func site(t *testing.T, opts search.Options, pages map[string]view) *collage.App {
	t.Helper()
	app, err := newSite(opts, pages)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func get(app *collage.App, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func build(t *testing.T, app *collage.App) (string, []search.Entry) {
	t.Helper()
	out := t.TempDir()
	b, err := collage.NewBuilder(app, collage.BuildOptions{OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, search.IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	var entries []search.Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("index is not JSON: %v\n%s", err, raw)
	}
	return out, entries
}

func pages() map[string]view {
	return map[string]view{
		"/caching": {Lang: "en", Title: "Caching", Description: "How caching works", Main: true,
			Body: `<p>collage caches <em>pages</em></p><p>and data.</p><h2 id="tags">Dependency tags</h2><p>Tags invalidate.</p><pre><code>code block</code></pre><h3>No id here</h3>`},
		"/tr/onbellek": {Lang: "tr", Title: "Önbellek", Main: true, Body: `<p>Çiçek ve ışık.</p>`},
		"/plain":       {Lang: "en", Title: "No main", Body: `<p>body text</p>`},
		"/hidden":      {Lang: "en", Title: "Hidden", NoIndex: true, Main: true, Body: `<p>secret</p>`},
		"/admin/users": {Lang: "en", Title: "Users", Main: true, Body: `<p>admin</p>`},
	}
}

func TestIndex(t *testing.T) {
	_, entries := build(t, site(t, search.Options{Exclude: []string{"/admin/"}}, pages()))
	byPath := map[string]search.Entry{}
	var paths []string
	for _, e := range entries {
		byPath[e.Path] = e
		paths = append(paths, e.Path)
	}
	if strings.Join(paths, " ") != "/caching /plain /tr/onbellek" {
		t.Fatalf("indexed %v: sorted, without the noindex page and the excluded prefix", paths)
	}

	c := byPath["/caching"]
	if c.Title != "Caching" || c.Description != "How caching works" || c.Locale != "en" {
		t.Errorf("entry = %+v", c)
	}
	if len(c.Headings) != 2 || c.Headings[0] != (search.Heading{ID: "tags", Text: "Dependency tags"}) || c.Headings[1].Text != "No id here" {
		t.Errorf("headings = %+v", c.Headings)
	}
	if c.Text != "Caching collage caches pages and data. Dependency tags Tags invalidate. No id here" {
		t.Errorf("text = %q", c.Text)
	}

	// Without <main>, the body — navigation, scripts, hidden and ignored
	// elements still left out.
	p := byPath["/plain"]
	if !strings.Contains(p.Text, "body text") || !strings.Contains(p.Text, "footer words") {
		t.Errorf("body fallback text = %q", p.Text)
	}
	for _, e := range entries {
		for _, never := range []string{"Home Docs", "notText", "code block", "ignored words", "Search the site"} {
			if strings.Contains(e.Text, never) {
				t.Errorf("%s: text holds %q: %q", e.Path, never, e.Text)
			}
		}
	}
	if tr := byPath["/tr/onbellek"]; tr.Locale != "tr" || tr.Text != "Önbellek Çiçek ve ışık." {
		t.Errorf("Turkish entry = %+v", tr)
	}
}

// Text is cut to MaxText characters, not bytes.
func TestMaxText(t *testing.T) {
	long := strings.Repeat("ğüşiöç ", 100)
	_, entries := build(t, site(t, search.Options{MaxText: 50}, map[string]view{
		"/long": {Lang: "en", Title: "L", Main: true, Body: "<p>" + long + "</p>"},
	}))
	if n := utf8.RuneCountInString(entries[0].Text); n > 50 || n < 40 || !utf8.ValidString(entries[0].Text) {
		t.Errorf("text of %d characters: %q", n, entries[0].Text)
	}
}

var scriptSrc = regexp.MustCompile(`<script src="(/_search/search\.[0-9a-f]{16}\.js)" defer></script>`)

func TestSearchBox(t *testing.T) {
	opts := search.Options{Locales: map[string]search.Text{"tr": {Label: "Ara", Unavailable: `Arama "dışa aktarılan" sitede`}}}
	app, err := collage.New(&collage.Config{
		Server: collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{
			"t/p.html": {Data: []byte(`<html><body>{{searchBox}}{{searchBox}}</body></html>`)},
		}, Root: "t"},
		Locale:  collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}},
		Plugins: []collage.Plugin{search.New(opts)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = app.RegisterPage(collage.NewPage("p").WithContent(collage.NewFragment("p", "p.html").Build()).WithPath("en", "/").WithPath("tr", "/").Build())

	body := get(app, "/").Body.String()
	for _, want := range []string{
		`<form class="search" role="search" hidden data-collage-search data-index="/search-index.json"`,
		`<label class="search-label" for="collage-search">Search</label>`,
		`<input class="search-input" id="collage-search" type="search" name="q"`,
		`aria-controls="collage-search-results" aria-describedby="collage-search-status"`,
		`<p class="search-status" id="collage-search-status" role="status" aria-live="polite"></p>`,
		`id="collage-search-2"`,
		`data-unavailable="Search is available on the exported site."`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("box lacks %s\n%s", want, body)
		}
	}
	m := scriptSrc.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no script:\n%s", body)
	}
	rec := get(app, m[1])
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") || !strings.Contains(rec.Body.String(), "data-collage-search") {
		t.Errorf("GET %s = %d %q", m[1], rec.Code, rec.Header().Get("Content-Type"))
	}

	body = get(app, "/tr").Body.String()
	if !strings.Contains(body, `>Ara</label>`) || !strings.Contains(body, `data-unavailable="Arama &#34;dışa aktarılan&#34; sitede"`) || !strings.Contains(body, `placeholder="Search the site"`) {
		t.Errorf("Turkish box:\n%s", body)
	}
}

// A server has no index: the address the script asks is not found, which the
// script shows as "search is available on the exported site".
func TestNoIndexOnAServer(t *testing.T) {
	app := site(t, search.Options{}, pages())
	if code := get(app, search.IndexPath).Code; code != http.StatusNotFound {
		t.Errorf("GET %s on a server = %d, want 404", search.IndexPath, code)
	}
}

// The built site holds the script its pages link.
func TestBuildWritesScript(t *testing.T) {
	out, _ := build(t, site(t, search.Options{}, pages()))
	page, err := os.ReadFile(filepath.Join(out, "caching", "index.html"))
	if err != nil {
		page, err = os.ReadFile(filepath.Join(out, "caching.html"))
	}
	if err != nil {
		t.Fatal(err)
	}
	m := scriptSrc.FindSubmatch(page)
	if m == nil {
		t.Fatalf("the built page links no script:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(string(m[1])))); err != nil {
		t.Errorf("the build did not write %s", m[1])
	}
}

func TestMisconfigurationStopsNew(t *testing.T) {
	for name, opts := range map[string]search.Options{
		"negative maxText":  {MaxText: -1},
		"a relative prefix": {Exclude: []string{"admin/"}},
	} {
		if _, err := newSite(opts, nil); err == nil {
			t.Errorf("%s: collage.New succeeded", name)
		}
	}
}

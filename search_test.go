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
	"unicode/utf8"

	search "github.com/Elagoht/collage-search"
	"github.com/Elagoht/collage/pkg/collage"
	"github.com/Elagoht/collage/pkg/collagetest"
)

const layout = `<!DOCTYPE html><html lang="{{.Lang}}"><head><title>{{.Title}} — Site</title><meta name="description" content="{{.Description}}">{{if .NoIndex}}<meta name="robots" content="noindex, follow">{{end}}</head>
<body><nav>Home Docs Blog</nav>{{searchBox}}
{{if .Main}}<main>{{end}}<h1>{{.Title}}</h1>{{.Body}}<script>var notText = 1;</script><div data-search-ignore>ignored words</div>{{if .Main}}</main>{{end}}<footer>footer words</footer></body></html>`

type view struct {
	Lang, Title, Description string
	Body                     string
	Main, NoIndex            bool
}

// pagesOf registers pages by path, "/tr/…" in Turkish, each named after its
// locale and path; a page under /admin/ is the page "admin" in both locales,
// at "/admin/…" and "/tr/admin/…".
func pagesOf(pages map[string]view) []collage.Registrable {
	admin := map[string]string{} // locale → path
	var adminData collage.Data
	var items []collage.Registrable
	for path, v := range pages {
		locale := "en"
		if strings.HasPrefix(path, "/tr/") {
			locale, path = "tr", strings.TrimPrefix(path, "/tr")
		}
		data := collage.Value(map[string]any{ // any: the template's data, of mixed types
			"Lang": v.Lang, "Title": v.Title, "Description": v.Description,
			"BodyHTML": template.HTML(v.Body), "Main": v.Main, "NoIndex": v.NoIndex,
		})
		if strings.HasPrefix(path, "/admin/") {
			admin[locale], adminData = path, data
			continue
		}
		name := locale + path
		items = append(items, collage.NewPage(name).
			WithContent(collage.NewFragment(name, "p.html").WithData(data).Build()).
			WithPath(locale, path).
			Build())
	}
	if len(admin) > 0 {
		b := collage.NewPage("admin").WithContent(collage.NewFragment("admin", "p.html").WithData(adminData).Build())
		for locale, path := range admin {
			b = b.WithPath(locale, path)
		}
		items = append(items, b.Build())
	}
	return items
}

func siteOptions(opts search.Options, pages map[string]view) []collagetest.SiteOption {
	return []collagetest.SiteOption{
		collagetest.WithoutHome(),
		collagetest.WithPlugins(search.New(opts)),
		collagetest.WithTemplates(map[string]string{"p.html": strings.ReplaceAll(layout, "{{.Body}}", "{{.BodyHTML}}")}),
		collagetest.WithConfig(func(c *collage.Config) {
			c.Locale = collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}}
		}),
		collagetest.WithRegister(pagesOf(pages)...),
	}
}

func site(t *testing.T, opts search.Options, pages map[string]view) *collage.App {
	t.Helper()
	return collagetest.NewSite(t, siteOptions(opts, pages)...).App
}

func get(app *collage.App, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func build(t *testing.T, app *collage.App) (string, []search.Entry) {
	t.Helper()
	out := t.TempDir()
	if _, err := collage.Build(context.Background(), app, collage.BuildOptions{OutDir: out}); err != nil {
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
		"/tr/onbellek":    {Lang: "tr", Title: "Önbellek", Main: true, Body: `<p>Çiçek ve ışık.</p>`},
		"/plain":          {Lang: "en", Title: "No main", Body: `<p>body text</p>`},
		"/hidden":         {Lang: "en", Title: "Hidden", NoIndex: true, Main: true, Body: `<p>secret</p>`},
		"/admin/users":    {Lang: "en", Title: "Users", Main: true, Body: `<p>admin</p>`},
		"/tr/admin/users": {Lang: "tr", Title: "Kullanıcılar", Main: true, Body: `<p>admin</p>`},
	}
}

func TestIndex(t *testing.T) {
	_, entries := build(t, site(t, search.Options{Exclude: []string{"admin"}}, pages()))
	byPath := map[string]search.Entry{}
	var paths []string
	for _, e := range entries {
		byPath[e.Path] = e
		paths = append(paths, e.Path)
	}
	if strings.Join(paths, " ") != "/caching /plain /tr/onbellek" {
		t.Fatalf("indexed %v: sorted, without the noindex page and the excluded page", paths)
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
	app := collagetest.NewSite(t,
		collagetest.WithoutHome(),
		collagetest.WithPlugins(search.New(opts)),
		collagetest.WithTemplates(map[string]string{"p.html": `<html><body>{{searchBox}}{{searchBox}}</body></html>`}),
		collagetest.WithConfig(func(c *collage.Config) {
			c.Locale = collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}}
		}),
		collagetest.WithRegister(collage.NewPage("p").WithContent(collage.NewFragment("p", "p.html").Build()).WithPath("en", "/").WithPath("tr", "/").Build()),
	).App

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
		"negative maxText": {MaxText: -1},
		"an empty page":    {Exclude: []string{""}},
	} {
		if _, err := collagetest.TrySite(t, siteOptions(opts, nil)...); err == nil || !strings.Contains(err.Error(), "search:") {
			t.Errorf("%s: err = %v, want a search: error", name, err)
		}
	}
}

// The box's words are under "text" now; the old top-level keys stop the app.
func TestTextKeys(t *testing.T) {
	_, err := collagetest.TrySite(t, append(siteOptions(search.Options{}, nil),
		collagetest.WithPluginConfig(search.Name, `{"label":"Find"}`))...)
	if err == nil || !strings.Contains(err.Error(), `unknown key "label"`) {
		t.Errorf("err = %v, want unknown key \"label\"", err)
	}
	app := collagetest.NewSite(t,
		collagetest.WithPlugins(search.New(search.Options{})),
		collagetest.WithPluginConfig(search.Name, `{"text":{"label":"Find"},"locales":{"tr":{"label":"Ara"}}}`),
		collagetest.WithTemplates(map[string]string{"home.html": `{{searchBox}}`}),
	).App
	if body := get(app, "/").Body.String(); !strings.Contains(body, `>Find</label>`) || !strings.Contains(body, `placeholder="Search the site"`) {
		t.Errorf("text.label not applied:\n%s", body)
	}
}

// exclude takes page names. The old prefix form ("/admin/") would match no page
// and silently index the pages it meant to hide, so it stops the app, as does a
// name no page has; a real name excludes.
func TestExcludeRefusesPrefixesAndUnknownNames(t *testing.T) {
	for exclude, want := range map[string]string{
		"/admin/": `search: exclude "/admin/": exclude takes page names ("admin") since v0.2.0, not path prefixes`,
		"/tags/":  `search: exclude "/tags/": exclude takes page names ("tags") since v0.2.0, not path prefixes`,
		"admni":   `search: exclude "admni": no page has that name`,
	} {
		_, err := collagetest.TrySite(t, siteOptions(search.Options{Exclude: []string{exclude}}, pages())...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("exclude %q: err = %v, want %q", exclude, err, want)
		}
	}
	_, entries := build(t, site(t, search.Options{Exclude: []string{"admin"}}, pages()))
	for _, e := range entries {
		if strings.Contains(e.Path, "/admin/") {
			t.Errorf("the excluded page admin is indexed at %s", e.Path)
		}
	}
}

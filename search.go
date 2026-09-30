// Package search is a collage plugin for search on a static site.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{search.New(search.Options{})},
//	})
//
// A template places the box:
//
//	{{searchBox}}
//
// When a static build has written every page, the plugin reads them back and
// writes search-index.json beside them: each page's path, title, description,
// headings and text. The box is a form and a list, hidden until the plugin's
// script — small, and dependent on nothing — takes them over: it fetches the index
// the first time the reader uses the box, and ranks pages by where the words
// appear, the title above the headings above the text.
//
// The index is a static build's: a running server has none, since nothing there
// has every page rendered. On a server the box says so quietly, rather than
// breaking.
package search

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"
	"unicode/utf8"

	"github.com/Elagoht/collage/pkg/collage"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Name is the plugin's name, and the key its configuration is found under.
const Name = "elagoht/search"

// IndexFile is the file a static build writes the index to, at the root of its
// output, and IndexPath where the site serves it.
const (
	IndexFile  = "search-index.json"
	IndexPath  = "/" + IndexFile
	ScriptPath = "/_search/search.js"
)

//go:embed search.js
var script []byte

// Options configures the plugin.
type Options struct {
	// MaxText is how many characters of a page's text the index keeps. Default
	// 5000: enough to find a page by what it says, without an index the size of
	// the site.
	MaxText int `json:"maxText"`
	// Exclude are path prefixes left out of the index: "/admin/", "/tags/".
	Exclude []string `json:"exclude"`
	// Text is what the box says.
	Text
	// Locales replaces Text for a locale; a field left empty keeps Text's.
	Locales map[string]Text `json:"locales"`
}

// Text is what the search box says.
type Text struct {
	// Label labels the field. Default "Search".
	Label string `json:"label"`
	// Placeholder is the field's placeholder. Default "Search the site".
	Placeholder string `json:"placeholder"`
	// Results announces how many pages were found, "{n}" standing for the
	// number; OneResult announces one. Default "{n} results" and "1 result".
	Results   string `json:"results"`
	OneResult string `json:"oneResult"`
	// NoResults says nothing was found. Default "No results".
	NoResults string `json:"noResults"`
	// Unavailable is shown where there is no index: on a server rather than on
	// the exported site. Default "Search is available on the exported site."
	Unavailable string `json:"unavailable"`
}

// Entry is one page of the index.
type Entry struct {
	Path        string    `json:"path"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Headings    []Heading `json:"headings,omitempty"`
	Text        string    `json:"text"`
	Locale      string    `json:"locale,omitempty"`
}

// Heading is one heading of a page: a result found in it links to it.
type Heading struct {
	ID   string `json:"id,omitempty"`
	Text string `json:"text"`
}

// Plugin writes the index and serves the script.
type Plugin struct {
	opts Options
}

// The hooks the plugin means to implement: a misspelt method would otherwise
// be a hook that silently never fires.
var (
	_ collage.Plugin            = (*Plugin)(nil)
	_ collage.Configurer        = (*Plugin)(nil)
	_ collage.BuildFinishedHook = (*Plugin)(nil)
)

// New returns a plugin with opts as its starting point, which the application's
// own configuration is then decoded over.
func New(opts Options) *Plugin { return &Plugin{opts: opts} }

func (p *Plugin) Name() string                   { return Name }
func (p *Plugin) Version() string                { return "0.1.2" }
func (p *Plugin) Shutdown(context.Context) error { return nil }

// Configure reads the configuration and adds {{searchBox}}.
func (p *Plugin) Configure(_ context.Context, host collage.ConfigHost) error {
	if err := host.Config(&p.opts); err != nil {
		return err
	}
	o := &p.opts
	if o.MaxText == 0 {
		o.MaxText = 5000
	}
	if o.MaxText < 0 {
		return fmt.Errorf("search: maxText %d: a length is positive", o.MaxText)
	}
	for _, prefix := range o.Exclude {
		if !strings.HasPrefix(prefix, "/") {
			return fmt.Errorf("search: exclude %q: a path prefix begins with /", prefix)
		}
	}
	defaults := Text{
		Label:       "Search",
		Placeholder: "Search the site",
		Results:     "{n} results",
		OneResult:   "1 result",
		NoResults:   "No results",
		Unavailable: "Search is available on the exported site.",
	}
	o.Text = merge(defaults, o.Text)
	return host.AddRenderFunc("searchBox", func(rc *collage.RenderContext) any { // any: html/template.FuncMap's own value type
		n := 0
		return func() (template.HTML, error) {
			n++
			return p.box(rc, n)
		}
	})
}

// merge is base with every field over sets.
func merge(base, over Text) Text {
	pick := func(a, b string) string {
		if b != "" {
			return b
		}
		return a
	}
	return Text{
		Label:       pick(base.Label, over.Label),
		Placeholder: pick(base.Placeholder, over.Placeholder),
		Results:     pick(base.Results, over.Results),
		OneResult:   pick(base.OneResult, over.OneResult),
		NoResults:   pick(base.NoResults, over.NoResults),
		Unavailable: pick(base.Unavailable, over.Unavailable),
	}
}

// Init serves the script.
func (p *Plugin) Init(_ context.Context, host collage.Host) error {
	if p.opts.MaxText == 0 {
		return errors.New("search: register the plugin in Config.Plugins, where Configure runs; {{searchBox}} needs it")
	}
	// fstest.MapFS is the standard library's in-memory file system: a mount needs
	// files that seek and a directory a build can walk, and it has both.
	return host.Mount("/_search/", fstest.MapFS{"search.js": {Data: script}})
}

// box renders the n-th search box of one render. It is hidden until the script
// takes it over: without the script it could only be a form that does nothing.
func (p *Plugin) box(rc *collage.RenderContext, n int) (template.HTML, error) {
	src, err := rc.Asset(ScriptPath)
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}
	text := p.opts.Text
	if l, ok := p.opts.Locales[rc.Locale]; ok {
		text = merge(text, l)
	}
	id := "collage-search"
	if n > 1 {
		id += "-" + strconv.Itoa(n)
	}
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<form class="search" role="search" hidden data-collage-search`)
	b.WriteString(` data-index="` + e(IndexPath) + `"`)
	b.WriteString(` data-results="` + e(text.Results) + `" data-one-result="` + e(text.OneResult) + `"`)
	b.WriteString(` data-no-results="` + e(text.NoResults) + `" data-unavailable="` + e(text.Unavailable) + `">`)
	b.WriteString(`<label class="search-label" for="` + id + `">` + e(text.Label) + `</label>`)
	b.WriteString(`<input class="search-input" id="` + id + `" type="search" name="q" autocomplete="off" spellcheck="false"`)
	b.WriteString(` placeholder="` + e(text.Placeholder) + `" aria-controls="` + id + `-results" aria-describedby="` + id + `-status">`)
	b.WriteString(`<p class="search-status" id="` + id + `-status" role="status" aria-live="polite"></p>`)
	b.WriteString(`<ol class="search-results" id="` + id + `-results"></ol>`)
	b.WriteString(`</form>`)
	b.WriteString(`<script src="` + e(src) + `" defer></script>`)
	return template.HTML(b.String()), nil // assembled here from escaped values
}

// OnBuildFinished reads every page the build wrote and writes the index beside
// them.
func (p *Plugin) OnBuildFinished(_ context.Context, ev *collage.BuildFinishedEvent) error {
	entries := make([]Entry, 0, len(ev.Files))
	for _, f := range ev.Files {
		if f.Kind != "page" || f.Name == "" || p.excluded(f.Path) {
			continue
		}
		body, err := os.ReadFile(f.File)
		if err != nil {
			return fmt.Errorf("search: read %s: %w", f.File, err)
		}
		doc, err := html.Parse(bytes.NewReader(body))
		if err != nil {
			continue // the tokenizer recovers from anything; only a reader fails here
		}
		entry, ok := p.entry(doc)
		if !ok {
			continue
		}
		entry.Path = f.Path
		if f.Locale != "" {
			entry.Locale = f.Locale
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	out, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if err := os.WriteFile(filepath.Join(ev.OutDir, IndexFile), out, 0o644); err != nil {
		return fmt.Errorf("search: %w", err)
	}
	return nil
}

func (p *Plugin) excluded(path string) bool {
	for _, prefix := range p.opts.Exclude {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// entry reads one page. A page asking not to be indexed is not.
func (p *Plugin) entry(doc *html.Node) (Entry, bool) {
	var (
		e                 Entry
		title, h1         string
		main, body, htmlN *html.Node
		noindex           bool
	)
	walk(doc, func(n *html.Node) bool {
		switch n.DataAtom {
		case atom.Html:
			htmlN = n
		case atom.Title:
			if title == "" && !inside(n, atom.Svg) {
				title = textOf(n)
			}
		case atom.Meta:
			switch strings.ToLower(attr(n, "name")) {
			case "description":
				e.Description = strings.TrimSpace(attr(n, "content"))
			case "robots":
				noindex = noindex || strings.Contains(strings.ToLower(attr(n, "content")), "noindex")
			}
		case atom.Main:
			if main == nil {
				main = n
			}
		case atom.Body:
			body = n
		}
		return true
	})
	if noindex {
		return Entry{}, false
	}
	scope := main
	if scope == nil {
		scope = body
	}
	if scope == nil {
		return Entry{}, false
	}
	if htmlN != nil {
		e.Locale = attr(htmlN, "lang")
	}
	var text strings.Builder
	walk(scope, func(n *html.Node) bool {
		if n.Type == html.ElementNode && skipped(n) {
			return false
		}
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
			text.WriteByte(' ')
			return false
		}
		switch n.DataAtom {
		case atom.H1:
			if h1 == "" {
				h1 = textOf(n)
			}
		case atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			if t := textOf(n); t != "" {
				e.Headings = append(e.Headings, Heading{ID: attr(n, "id"), Text: t})
			}
		}
		// Block elements end words: "<p>one</p><p>two</p>" is two words.
		text.WriteByte(' ')
		return true
	})
	// The page's own heading names it better than its <title>, which usually
	// carries the site's name too.
	e.Title = h1
	if e.Title == "" {
		e.Title = title
	}
	e.Text = truncate(strings.Join(strings.Fields(text.String()), " "), p.opts.MaxText)
	return e, true
}

// skipped reports an element whose text is not the page's: scripts and styles,
// code blocks — long, and what they say is usually in the prose around them —
// navigation repeated on every page, what is hidden — the search box itself, until
// its script runs — and anything marked data-search-ignore.
func skipped(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Nav, atom.Pre:
		return true
	}
	for _, a := range n.Attr {
		if a.Key == "data-search-ignore" || a.Key == "hidden" {
			return true
		}
	}
	return false
}

// truncate cuts s to at most max characters, at a word when one is near.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)[:max]
	cut := string(runes)
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)*9/10 {
		cut = cut[:i]
	}
	return cut
}

// walk visits n and its descendants in document order; visit returning false
// skips a node's children.
func walk(n *html.Node, visit func(*html.Node) bool) {
	if !visit(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func inside(n *html.Node, a atom.Atom) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.DataAtom == a {
			return true
		}
	}
	return false
}

// textOf is n's text content, whitespace collapsed, scripts and styles left out.
func textOf(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) bool {
		if c.Type == html.ElementNode && (c.DataAtom == atom.Script || c.DataAtom == atom.Style || c.DataAtom == atom.Template) {
			return false
		}
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
		return true
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

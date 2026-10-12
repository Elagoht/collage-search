# elagoht/search

A collage plugin for search on a static site: a static build writes an index of
every page, and a small script searches it in the browser, with no server behind it.

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{search.New(search.Options{})},
})
```

```html
{{searchBox}}
```

Requires collage v0.61.0 or later. Register it in `Config.Plugins`: it adds a
template function, which only a plugin registered there can.

**The index is a static build's.** It is written by `collage export`, and a running
server — `collage dev`, `collage serve` — has none: see [On a server](#on-a-server).

## The index

When a static build has written every page, the plugin reads each one back and
writes `search-index.json` at the root of the output, beside them:

```json
[
  {
    "path": "/docs/caching/",
    "title": "Caching",
    "description": "How collage caches rendered pages.",
    "headings": [{ "id": "dependency-tags", "text": "Dependency tags" }],
    "text": "collage caches two things. The page cache stores…",
    "locale": "en"
  }
]
```

| Field | From |
| --- | --- |
| `path` | the URL path the page answers |
| `title` | the page's `<h1>`, or its `<title>` when it has none — a `<title>` usually carries the site's name too |
| `description` | `<meta name="description">` |
| `headings` | every `h2` to `h6`, with its `id`, so a result found in a heading links to it |
| `text` | the page's text, whitespace collapsed, cut to `maxText` characters |
| `locale` | the locale the page was built in |

Everything is read from `<main>`, or from `<body>` on a page without one, leaving
out scripts, styles, code blocks (`<pre>`) — long, and usually explained by the
prose around them — `<nav>` — the same links on every page would find every page —
anything `hidden`, and anything marked `data-search-ignore`:

```html
<aside data-search-ignore>Related posts…</aside>
```

A page with `<meta name="robots" content="noindex">` is left out, and so is every
page named in `exclude`, in every locale. Error pages are not indexed.

## The box

`{{searchBox}}` renders a labelled search field, a status line assistive technology
announces, and a list for the results:

```html
<form class="search" role="search" hidden data-collage-search data-index="/search-index.json" …>
  <label class="search-label" for="collage-search">Search</label>
  <input class="search-input" id="collage-search" type="search" name="q" …>
  <p class="search-status" id="collage-search-status" role="status" aria-live="polite"></p>
  <ol class="search-results" id="collage-search-results"></ol>
</form>
<script src="/_search/search.<hash>.js" defer></script>
```

It is `hidden` until the script takes it over: without the script it could only be a
form that does nothing. Two boxes on one page get their own ids. The classes are
there to style; the plugin ships no CSS.

## The script

`/_search/search.js` — a few kilobytes, dependent on nothing, linked by its
content-addressed name so a browser keeps it for a year. It fetches the index the
first time the reader focuses the box, not with the page, and then:

- **Matches words.** The query is split into words, and a page must contain every
  one, whole or as the start of or a part of a word. Case and accents are ignored —
  `cicek` finds `çiçek` — and lower case is the page's language's, so on a Turkish
  page `I` is `ı`.
- **Ranks** a word found in the title above one found in a heading, above the
  description, above the text; a whole word above a part of one; the whole query
  as a phrase above its words apart. The ten best are shown.
- **Links** a result found in a heading to that heading.
- **Shows** a stretch of the text around the words, marked with `<mark>`, and the
  number found in the status line. Enter goes to the best result; Escape clears.
- **Keeps to the page's language**: only pages whose locale is the language of
  `<html lang>` are searched — `en-GB` matches `en` — unless the index has none in
  it, when every page is.

## On a server

A running server has no index: nothing there has every page rendered, and a plugin
cannot render every page on demand. The script asks for `/search-index.json`, is
told it is not found, and the box says so, quietly — "Search is available on the
exported site." — instead of breaking. Try search on the output of
`collage export`, served by any static file server.

## Options

| Option | JSON | Default | |
| --- | --- | --- | --- |
| `MaxText` | `maxText` | `5000` | characters of each page's text the index keeps |
| `Exclude` | `exclude` | | names of pages left out of the index, in every locale |
| `Text` | `text` | | what the box says, below |
| `Locales` | `locales` | | a `text` per locale, by the page's locale; a field left empty keeps `text`'s |

`text` (a `search.Text`) holds:

| Key | Default | |
| --- | --- | --- |
| `label` | `Search` | the field's label |
| `placeholder` | `Search the site` | the field's placeholder |
| `results` | `{n} results` | the status line; `{n}` is the number |
| `oneResult` | `1 result` | the status line for one |
| `noResults` | `No results` | the status line for none |
| `unavailable` | `Search is available on the exported site.` | where there is no index |

```go
search.New(search.Options{
	Text: search.Text{Label: "Find"},
	Locales: map[string]search.Text{
		"tr": {Label: "Ara", Placeholder: "Sitede ara", Results: "{n} sonuç", OneResult: "1 sonuç",
			NoResults: "Sonuç yok", Unavailable: "Arama, dışa aktarılan sitede çalışır."},
	},
})
```

## Configuration

```json
{
  "elagoht/search": {
    "maxText": 5000,
    "exclude": ["admin", "tags"],
    "text": { "label": "Search" },
    "locales": { "tr": { "label": "Ara", "noResults": "Sonuç yok" } }
  }
}
```

A negative `maxText` and an empty name in `exclude` stop `collage.New`. So does a
name in `exclude` that begins with `/` (the old prefix form) or that no registered
page has, so a typo is not a page silently indexed.

## Limitations

- **No index on a server**, as above. A site served by collage in production, rather
  than exported, has no search from this plugin.
- The index is one file of every page, fetched whole. It suits a site of hundreds
  of pages; at many thousands, lower `maxText` or use a search service.
- The index is at `/search-index.json`: a site exported to a subdirectory of its
  host would need the file at its root.
- Matching is by substring, not by stem: `running` does not find `run`'s page unless
  it also says `running`. Chinese and Japanese text, written without spaces, is
  matched as whole phrases.

## Changes

### Changed in v0.2.0

| Was | Now |
| --- | --- |
| `label`, `placeholder`, `results`, `oneResult`, `noResults`, `unavailable` at the top level (embedded `Text`) | the same keys under `"text"` (`Options.Text`) |
| `exclude` as path prefixes (`"/admin/"`), which missed a locale's prefixed path (`/tr/admin/`) | `exclude` as page names (`"admin"`), in every locale |
| `search.ScriptPath` | unexported; `{{searchBox}}` links the script |

An old prefix in `exclude` stops the app rather than matching nothing:
`search: exclude "/admin/": exclude takes page names ("admin") since v0.2.0, not path prefixes`.
A name no page has stops it too: `search: exclude "x": no page has that name`.

A top-level `label` (or any of the five others) now stops the app with
`unknown key "label"`. Requires collage v0.61.0.

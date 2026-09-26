// elagoht/search: searches the index a static build wrote, in the browser.
// It takes over every <form data-collage-search> {{searchBox}} rendered.
(function () {
  "use strict";

  var LIMIT = 10;

  // fold lowers text as the page's language does — Turkish "I" is "ı" — and
  // takes accents away, so "cicek" finds "çiçek". Anything that is not a letter
  // or a digit becomes a space, which is what makes word starts findable.
  function fold(text, lang) {
    return (" " + lower(text, lang).normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .replace(/ı/g, "i")
      .replace(/[^\p{L}\p{N}]+/gu, " ") + " ");
  }

  function tokens(query, lang) {
    return fold(query, lang).split(" ").filter(Boolean).slice(0, 10);
  }

  function lower(text, lang) {
    try {
      return String(text).toLocaleLowerCase(lang || undefined);
    } catch (e) {
      return String(text).toLowerCase();
    }
  }

  // written is the query's words as typed, lowered but not folded: what a
  // snippet looks for in the text as it is written, before the folded words,
  // which find "çiçek" in "cicek" only when the text has no accents either.
  function written(query, lang) {
    return lower(query, lang).split(/[^\p{L}\p{N}]+/u).filter(Boolean).concat(tokens(query, lang));
  }

  function prepare(entries, lang) {
    return entries.map(function (e) {
      return {
        entry: e,
        title: fold(e.title || "", lang),
        description: fold(e.description || "", lang),
        headings: (e.headings || []).map(function (h) { return fold(h.text, lang); }),
        text: fold(e.text || "", lang)
      };
    });
  }

  // score ranks one page for the query's words: every word must appear
  // somewhere, and counts for more where it matters more — the title over the
  // headings over the description over the text, a whole word over a part of
  // one. It returns null for a page missing a word, and otherwise the score and
  // the heading a result should link to.
  function score(p, words, phrase) {
    var total = 0;
    var heading = -1;
    for (var i = 0; i < words.length; i++) {
      var w = words[i];
      var start = " " + w;
      var whole = " " + w + " ";
      var s = 0;
      if (p.title.indexOf(w) >= 0) {
        s = 10 + (p.title.indexOf(start) >= 0 ? 5 : 0) + (p.title.indexOf(whole) >= 0 ? 5 : 0);
      } else {
        for (var h = 0; h < p.headings.length; h++) {
          if (p.headings[h].indexOf(w) >= 0) {
            s = 5 + (p.headings[h].indexOf(start) >= 0 ? 2 : 0);
            if (heading < 0) heading = h;
            break;
          }
        }
        if (!s && p.description.indexOf(w) >= 0) s = 3;
        if (!s && p.text.indexOf(w) >= 0) s = 1 + (p.text.indexOf(start) >= 0 ? 1 : 0);
      }
      if (!s) return null;
      total += s;
    }
    if (words.length > 1) {
      if (p.title.indexOf(phrase) >= 0) total += 10;
      else if (p.text.indexOf(phrase) >= 0) total += 3;
    }
    if (heading < 0) {
      for (var k = 0; k < p.headings.length; k++) {
        if (p.headings[k].indexOf(words[0]) >= 0) { heading = k; break; }
      }
    }
    return { score: total, heading: heading };
  }

  // rank returns the best pages for query, at most limit of them.
  function rank(prepared, query, lang, limit) {
    var words = tokens(query, lang);
    if (!words.length) return [];
    var phrase = " " + words.join(" ");
    var found = [];
    for (var i = 0; i < prepared.length; i++) {
      var r = score(prepared[i], words, phrase);
      if (r) found.push({ page: prepared[i], score: r.score, heading: r.heading });
    }
    found.sort(function (a, b) {
      return b.score - a.score || (a.page.entry.title || "").localeCompare(b.page.entry.title || "");
    });
    return found.slice(0, limit || LIMIT).map(function (f) {
      var e = f.page.entry;
      var h = f.heading >= 0 ? e.headings[f.heading] : null;
      return {
        path: e.path + (h && h.id ? "#" + encodeURIComponent(h.id) : ""),
        title: e.title || e.path,
        heading: h ? h.text : "",
        snippet: snippet(e, written(query, lang), lang)
      };
    });
  }

  // snippet is a stretch of the page's text around the first word found in it,
  // or its description when the words are only in the title.
  function snippet(e, words, lang) {
    var text = e.text || "";
    var low = lower(text, lang);
    var at = -1;
    for (var i = 0; i < words.length && at < 0; i++) at = low.indexOf(words[i]);
    if (at < 0) return e.description || text.slice(0, 160);
    var from = Math.max(0, at - 60);
    var to = Math.min(text.length, at + 120);
    if (from > 0) { var sp = text.indexOf(" ", from); if (sp >= 0 && sp < at) from = sp + 1; }
    if (to < text.length) { var ep = text.lastIndexOf(" ", to); if (ep > at) to = ep; }
    return (from > 0 ? "… " : "") + text.slice(from, to) + (to < text.length ? " …" : "");
  }

  // base is the language an index entry or a page is in, without a region.
  function base(lang) {
    return String(lang || "").toLowerCase().split("-")[0];
  }

  var loads = {};
  function load(url) {
    if (!loads[url]) {
      loads[url] = fetch(url, { headers: { Accept: "application/json" } }).then(function (res) {
        if (!res.ok) throw new Error("no index");
        return res.json();
      });
    }
    return loads[url];
  }

  function mark(el, text, words, lang) {
    // Words are marked in the text as it is written, without folding: a mark
    // around the wrong letters would be worse than none.
    var low = lower(text, lang);
    var i = 0;
    while (i < text.length) {
      var next = -1, len = 0;
      for (var w = 0; w < words.length; w++) {
        var at = low.indexOf(words[w], i);
        if (at >= 0 && (next < 0 || at < next)) { next = at; len = words[w].length; }
      }
      // A language whose lower case changes a text's length would put the
      // marks out of place; such a text is shown unmarked.
      if (next < 0 || !len || low.length !== text.length) break;
      el.appendChild(document.createTextNode(text.slice(i, next)));
      var m = document.createElement("mark");
      m.textContent = text.slice(next, next + len);
      el.appendChild(m);
      i = next + len;
    }
    el.appendChild(document.createTextNode(text.slice(i)));
  }

  function enhance(form) {
    if (form.getAttribute("data-collage-search-ready")) return;
    form.setAttribute("data-collage-search-ready", "1");
    var input = form.querySelector("input[type=search]");
    var status = form.querySelector(".search-status");
    var list = form.querySelector(".search-results");
    if (!input || !status || !list) return;
    var lang = document.documentElement.lang || "";
    var prepared = null;
    var unavailable = false;
    var timer = 0;
    var d = form.dataset;

    function ready() {
      if (prepared || unavailable) return Promise.resolve();
      return load(d.index).then(function (entries) {
        var mine = entries.filter(function (e) { return base(e.locale) === base(lang); });
        prepared = prepare(mine.length ? mine : entries, lang);
      }, function () {
        unavailable = true;
      });
    }

    function show() {
      list.textContent = "";
      var query = input.value.trim();
      if (!query) { status.textContent = ""; return; }
      if (unavailable) { status.textContent = d.unavailable; return; }
      if (!prepared) return;
      var results = rank(prepared, query, lang, LIMIT);
      var words = written(query, lang);
      status.textContent = !results.length ? d.noResults
        : results.length === 1 ? d.oneResult
          : d.results.replace("{n}", String(results.length));
      results.forEach(function (r) {
        var li = document.createElement("li");
        var a = document.createElement("a");
        a.href = r.path;
        a.textContent = r.heading ? r.title + " › " + r.heading : r.title;
        li.appendChild(a);
        if (r.snippet) {
          var p = document.createElement("p");
          mark(p, r.snippet, words, lang);
          li.appendChild(p);
        }
        list.appendChild(li);
      });
    }

    input.addEventListener("focus", ready);
    input.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(function () { ready().then(show); }, 80);
    });
    input.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape") { input.value = ""; show(); }
    });
    // Enter goes to the best result: there is no results page to submit to.
    form.addEventListener("submit", function (ev) {
      ev.preventDefault();
      ready().then(function () {
        show();
        var first = list.querySelector("a");
        if (first) window.location.href = first.href;
      });
    });
    form.hidden = false;
  }

  if (typeof document !== "undefined") {
    var forms = document.querySelectorAll("form[data-collage-search]");
    for (var i = 0; i < forms.length; i++) enhance(forms[i]);
  }
  if (typeof module !== "undefined" && module.exports) {
    module.exports = { fold: fold, tokens: tokens, prepare: prepare, rank: rank };
  }
})();

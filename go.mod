// A collage plugin for search on a static site: a static build writes an index of
// every page, and a small script searches it in the browser, with no server
// behind it.
module github.com/Elagoht/collage-search

go 1.26.0

require github.com/Elagoht/collage v0.50.0

require golang.org/x/net v0.59.0

retract v0.1.4 // tagged at v0.1.3's commit by mistake

# Documentation source

The public documentation uses the same Jekyll/kramdown stack and reading layout
as [Gophling](https://github.com/y0f/gophling/tree/main/docs), with Asura's original
logo and a grayscale/blue palette. It is independent of the Go application.

Use Ruby 3.4.12 and Bundler 4.0.24. Direct dependencies and transitive checksums
are pinned in `Gemfile` and `Gemfile.lock`.

```sh
cd docs
gem install bundler -v 4.0.24 --no-document
bundle _4.0.24_ install
JEKYLL_ENV=production bundle _4.0.24_ exec jekyll build --strict_front_matter --destination ../.build/docs
```

The verified build image is
`ruby:3.4.12-bookworm@sha256:d7c312a48736dd291d7ce043c847f04dfde9bdf7334491ca6a545a4a184395be`.

`_data/navigation.yml` is the public page catalog. Every page has an explicit
permalink, description and stable heading IDs. Preserve old anchors when
reorganizing content. Page links and metadata use the configured `url` and
`baseurl`; test with both the default `/asura` path and an empty base path.

Only the built `.build/docs` directory is a publishing artifact. Internal review
documents, this README, dependency files and historical source directories are
excluded from the site. Ordinary navigation, page contents and disclosures work
without JavaScript; `assets/bookmarks.js` only restores old hash-based bookmarks.

# Documentation source

The public documentation uses Jekyll/kramdown and plain CSS, following
[Gophling](https://github.com/y0f/gophling/tree/main/docs). It builds independently
of the Go application.

Use Ruby 3.4.12 and Bundler 4.0.24. Direct dependencies and transitive checksums
are pinned in `Gemfile` and `Gemfile.lock`.

```sh
cd docs
gem install bundler -v 4.0.24 --no-document
bundle _4.0.24_ install
cd ..
make docs-check
```

This builds production HTML into `.build/docs` and checks local links, anchors,
metadata, the sitemap and allowed public files. Without Make, use PowerShell:

```powershell
$env:JEKYLL_ENV = 'production'
Push-Location docs
bundle exec jekyll build --strict_front_matter --destination ../.build/docs
Pop-Location
go run ./cmd/docscheck -dir .build/docs
```

For a preview, run `bundle exec jekyll serve --strict_front_matter --destination ../.build/docs`
from `docs`, then open `http://127.0.0.1:4000/asura/`.

`_data/navigation.yml` is the public page catalog. Every page has an explicit
permalink, description and stable heading IDs. Preserve old anchors when
reorganizing content. Page links and metadata use the configured `url` and
`baseurl`; test with both the default `/asura` path and an empty base path.
For another host, set the HTTPS origin as `url` and the deployment prefix as
`baseurl` in `_config.yml`.

Publish only the checked `.build/docs` artifact. `_config.yml` excludes this
README and dependency manifests; the checker rejects unexpected public files.
Keep private notes outside `docs`. Navigation, page contents and disclosures
work without JavaScript; `assets/bookmarks.js` restores old hash-based bookmarks.

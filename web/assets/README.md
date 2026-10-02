# Embedded browser assets

These files are vendored and compiled into the Jikko Go binary with `go:embed`.

- `htmx.min.js`: `htmx.org` 4.0.0 `dist/htmx.min.js` (HTMX plus its bundled core extensions, including `hx-live` and `hx-sse`).
- `basecoat.min.css`: `basecoat-css` 1.0.2 `dist/basecoat.cdn.min.css` (default Vega bundle).

They are runtime assets, not a Node dependency. Updating them should be an explicit version bump: fetch the pinned upstream distributions, replace these files, run the Go tests, and review the resulting diff/size change.

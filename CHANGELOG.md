# Changelog

## v1.0.3

- Hidden paths (`--exclude`, `--no-dotfiles`, `--no-dirs`) now get the same response as missing ones, including the custom `404.html` and SPA fallback, so their existence is not revealed.
- Only regular files are served; requesting a FIFO or device no longer hangs.
- `--watch` no longer redirects unclean paths such as `/a/../b.txt`.
- `--cache 0` sends `Cache-Control: public, max-age=0` instead of no header, which let browsers cache heuristically.
- Basic auth compares username and password unconditionally, so timing does not reveal a correct username.
- CORS preflights allow the headers they request (`Range`, `X-Requested-With`, ...).
- Live reload script is injected before `</BODY>` regardless of case.
- `--ext .html` works like `--ext html`.
- The banner lists LAN addresses when bound to `::`.
- Directory redirects can no longer point to another host; they keep the query string and escape names.
- Paths through a file, with NUL bytes or over-long names return 404 instead of 500.
- Listing links work for filenames containing colons; `--dir-size` skips hidden entries.
- Invalid `--exclude` patterns and unreadable TLS certificates are rejected at startup.
- CORS runs outside auth so preflights work, and `Authorization` is an allowed header.
- `--no-dotfiles` also stops dotfiles from triggering live reload.
- Faster large-file responses with `--no-gzip` (sendfile) and faster `--dir-size` without `--symlinks`.

## v1.0.2

- Enforce file visibility rules and improve live reload.

## v1.0.1

- Module path is `github.com/ivalkenburg/goblet`, so `go install` works.
- Release assets are named `goblet-{darwin,linux}-{amd64,arm64}`; adds linux/arm64.
- Timing-safe basic auth, `--watch` skips excluded directories, `--dir-size` respects `--symlinks`, CORS allows all common methods.

## v1.0.0

- Initial release.

# goblet

Go static file server for local development (like npm `http-server`). Single `main` package, cobra for flags.

## Layout

- `main.go`: cobra root command; `version` set via ldflags.
- `config.go`: `Config` struct and all flag registration. New flag = field + flag here + README table.
- `server.go`: `Start` validates config, binds, prints banner, serves, graceful shutdown on SIGINT/SIGTERM (5 s drain).
- `handler.go`: middleware chain, outermost first: logging → auth → [mux: SSE | gzip → cache → cors → inject → file]. Basic auth compares in constant time.
- `listing.go` + `listing.html` (embedded): directory listing UI, breadcrumbs, `--dir-size`.
- `reload.go`: `--watch`. fsnotify watcher (recursive, skips `--exclude` dirs) → SSE endpoint; script injected before `</body>` in 200 `text/html` responses.
- Tests next to each file (`*_test.go`).

## Commands

```sh
go test ./...
go vet ./...
go build -o goblet .
```

## Invariants

- `--exclude`, `--no-dotfiles`, `--no-dirs` must both hide from listings and block direct requests (404). Keep the watcher consistent with `--exclude`.
- Symlinks are only followed with `--symlinks`, including in `--dir-size`.
- Wrapped `ResponseWriter`s must forward `Flush` so SSE works through the chain.

## Releasing

1. Push a `vX.Y.Z` tag. `.github/workflows/release.yml` builds `goblet-{darwin,linux}-{amd64,arm64}` and `goblet-windows-amd64.exe` with `main.version=X.Y.Z` and creates the release.
2. Update `Formula/goblet.rb` in `ivalkenburg/homebrew-tap`: version and the four darwin/linux sha256s.

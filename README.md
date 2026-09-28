# goblet

A static file server for local development. Run it in a folder and that folder
is on `http://localhost:8080`.

It does the same job as the [`http-server`](https://github.com/http-party/http-server)
npm package, but it's a single Go binary, so you don't need Node installed to
look at a build folder or share a few files on your network.

## Features

- Directory listings with breadcrumb navigation
- Gzip compression and configurable `Cache-Control`
- Live reload of connected browsers when files change
- SPA mode, which serves `index.html` for unknown routes
- Basic auth, HTTPS with your own certificate, and CORS
- Hiding dotfiles, directories or any glob pattern, such as `*.env` or `node_modules`
- `/about` resolves to `about.html`, and a `404.html` in the root replaces the default 404 page
- Apache-style access logs, and opening the browser on start

## Install

With Homebrew:

```sh
brew install ivalkenburg/tap/goblet
```

With Go:

```sh
go install github.com/ivalkenburg/goblet@latest
```

From source:

```sh
git clone https://github.com/ivalkenburg/goblet.git
cd goblet
go build -o goblet .
```

## Usage

```
goblet [path] [flags]
```

With no path, goblet serves the current directory on port 8080.

```sh
goblet ./dist --spa                          # a single-page app build
goblet ./src --watch                         # reload the browser on changes
goblet -p 0                                  # any free port, shown at startup
goblet ./private --username admin --password secret
goblet --tls --cert cert.pem --key key.pem
goblet ./project --exclude '*.env' --exclude node_modules
```

| Flag | Default | What it does |
| --- | --- | --- |
| `-p`, `--port` | `8080` | Port to listen on; `0` picks a free one |
| `-a`, `--address` | all interfaces | Address to bind to |
| `-o`, `--open` | off | Open the browser after starting |
| `-w`, `--watch` | off | Live-reload browsers when files change |
| `--spa` | off | Serve `index.html` for unmatched paths |
| `-e`, `--ext` | `html` | Extension tried for extensionless URLs |
| `-d`, `--no-listing` | off | Turn off directory listings |
| `--no-dirs` | off | Hide directories and return 404 for their URLs and contents |
| `--no-dotfiles` | off | Hide dotfiles and deny access to them |
| `--exclude` | | Glob to hide and block; repeatable |
| `--dir-size` | off | Show total directory sizes in listings |
| `--symlinks` | off | Follow symbolic links |
| `-c`, `--cache` | `-1` | `Cache-Control` max-age in seconds; `-1` turns caching off |
| `--no-gzip` | off | Turn off gzip |
| `--cors` | off | Send `Access-Control-Allow-Origin: *` |
| `--username`, `--password` | | Basic auth; set both |
| `-S`, `--tls` | off | Serve HTTPS |
| `-C`, `--cert` | `cert.pem` | TLS certificate |
| `-K`, `--key` | `key.pem` | TLS private key |
| `-t`, `--timeout` | `120` | Connection timeout in seconds; `0` turns it off |
| `-s`, `--silent` | off | No log output |
| `--utc` | off | UTC timestamps in logs |

Shell completions: Homebrew installs them. Otherwise see `goblet completion --help`.

## License

MIT. See [LICENSE](LICENSE).

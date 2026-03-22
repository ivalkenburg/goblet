# goblet

A fast, zero-config HTTP file server for local development. Inspired by the [`http-server`](https://github.com/http-party/http-server) npm package, built in Go.

## Features

- **Directory listing** — clean HTML UI with breadcrumb navigation, sorted dirs and files
- **Gzip compression** — automatic content encoding for supported clients
- **Cache control** — configurable `Cache-Control` headers, or disabled entirely
- **Basic authentication** — protect your server with a username and password
- **TLS/HTTPS** — serve over HTTPS with your own certificate and key
- **CORS** — add permissive `Access-Control-Allow-Origin: *` headers
- **SPA mode** — serve `index.html` for all unmatched routes (client-side routing)
- **Default extension** — resolve `/about` → `/about.html` automatically
- **Custom 404 page** — place a `404.html` in the root to use it
- **Dotfile protection** — hide and deny access to dotfiles
- **Directory blocking** — hide subdirectories from listing and return 404 for any directory path
- **Directory sizes** — optionally calculate and display total size of each directory in listings
- **Exclude patterns** — hide and block files/directories by glob pattern (e.g. `*.env`, `node_modules`)
- **Symlink support** — optionally follow symbolic links
- **Live reload** — watch for file changes and automatically reload connected browsers
- **Access logging** — Apache-style logs with IP, method, path, status, and elapsed time
- **Browser auto-open** — launch the browser automatically on start
- **Graceful shutdown** — handles `SIGINT`/`SIGTERM` with a 5-second drain

## Installation

**From source:**

```sh
go install goblet@latest
```

**Or clone and build:**

```sh
git clone https://github.com/yourname/goblet
cd goblet
go build -o goblet .
```

## Usage

```
goblet [path] [flags]
```

Serve the current directory on port 8080:

```sh
goblet
```

Serve a specific directory:

```sh
goblet ./dist
```

Serve on a different port:

```sh
goblet -p 3000
```

## Flags

| Flag            | Short | Default    | Description                                                     |
| --------------- | ----- | ---------- | --------------------------------------------------------------- |
| `--port`        | `-p`  | `8080`     | Port to listen on (`0` picks a random free port)                |
| `--address`     | `-a`  | _(all)_    | Address to bind to                                              |
| `--no-listing`  | `-d`  | `false`    | Disable directory listing                                       |
| `--silent`      | `-s`  | `false`    | Suppress all log output                                         |
| `--no-gzip`     |       | `false`    | Disable gzip compression                                        |
| `--cache`       | `-c`  | `-1`       | Cache `max-age` in seconds (`-1` disables caching)              |
| `--username`    |       |            | Username for basic auth (requires `--password`)                 |
| `--password`    |       |            | Password for basic auth (requires `--username`)                 |
| `--tls`         | `-S`  | `false`    | Enable TLS/HTTPS                                                |
| `--cert`        | `-C`  | `cert.pem` | Path to TLS certificate                                         |
| `--key`         | `-K`  | `key.pem`  | Path to TLS private key                                         |
| `--cors`        |       | `false`    | Enable CORS (`Access-Control-Allow-Origin: *`)                  |
| `--no-dotfiles` |       | `false`    | Hide dotfiles and deny access to them                           |
| `--no-dirs`     |       | `false`    | Hide directories from listing and return 404 for directory paths |
| `--exclude`     |       |            | Glob pattern to hide/block (repeatable, e.g. `--exclude '*.env'`) |
| `--timeout`     | `-t`  | `120`      | Connection timeout in seconds (`0` to disable)                  |
| `--ext`         | `-e`  | `html`     | Default extension for extensionless URLs                        |
| `--open`        | `-o`  | `false`    | Open browser after starting                                     |
| `--utc`         |       | `false`    | Use UTC timestamps in logs                                      |
| `--symlinks`    |       | `false`    | Follow symbolic links                                           |
| `--spa`         |       | `false`    | SPA mode — serve `index.html` for unmatched paths               |
| `--watch`       | `-w`  | `false`    | Watch for file changes and live-reload browsers                 |
| `--dir-size`    |       | `false`    | Calculate and display total size of directories in listings     |

## Examples

**Serve a React/Vue/Svelte build with SPA routing:**

```sh
goblet ./dist --spa
```

**Password-protect a directory:**

```sh
goblet ./private --username admin --password secret
```

**Serve over HTTPS:**

```sh
goblet --tls --cert cert.pem --key key.pem
```

**Share files on the local network with caching enabled:**

```sh
goblet ./files --cache 3600
```

**Serve a static site quietly (no logs) with browser auto-open:**

```sh
goblet ./site --silent --open
```

**Pick a random free port (printed in the banner):**

```sh
goblet -p 0
```

**Serve a flat file collection with no directory navigation:**

```sh
goblet ./downloads --no-dirs
```

**Exclude sensitive files and heavy directories from serving and listing:**

```sh
goblet ./project --exclude '*.env' --exclude 'node_modules'
```

**Develop with live reload:**

```sh
goblet ./src --watch
```

**Show directory sizes in the listing:**

```sh
goblet ./files --dir-size
```

package main

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/klauspost/compress/gzhttp"
)

// buildHandler chains all configured middleware around the core file handler.
// Order (outermost → innermost): logging → cors → auth → [mux: SSE | gzip → cache → inject → file]
// CORS sits outside auth because browsers never send credentials on preflights.
func buildHandler(cfg *Config, r *reloader) http.Handler {
	var fileH http.Handler = &fileHandler{cfg: cfg}
	if r != nil {
		fileH = liveReloadMiddleware(fileH)
	}
	fileH = cacheMiddleware(fileH, cfg)
	if !cfg.NoGzip {
		fileH = gzhttp.GzipHandler(fileH)
	}

	// When live reload is active, route /_goblet/reload to the SSE handler
	// so it bypasses gzip and cache (but still goes through auth and logging).
	// A plain path check is used instead of http.ServeMux, which would
	// redirect unclean paths and make --watch change how files are served.
	h := fileH
	if r != nil {
		h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == reloadSSEPath {
				r.ServeHTTP(w, req)
				return
			}
			fileH.ServeHTTP(w, req)
		})
	}

	if cfg.Username != "" {
		h = basicAuthMiddleware(h, cfg.Username, cfg.Password)
	}
	if cfg.CORS {
		h = corsMiddleware(h)
	}
	if !cfg.Silent {
		h = loggingMiddleware(h, cfg)
	}
	return h
}

// statusRecorder wraps http.ResponseWriter to capture the HTTP status code
// for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher so streaming responses work through the recorder.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ReadFrom forwards to the underlying writer so http.ServeContent can still
// use sendfile when nothing between it and the connection rewrites the body.
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(writerOnly{r.ResponseWriter}, src)
}

// writerOnly hides any ReadFrom method so io.Copy cannot recurse into it.
type writerOnly struct{ io.Writer }

func loggingMiddleware(next http.Handler, cfg *Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		ts := start
		if cfg.UTC {
			ts = start.UTC()
		}
		fmt.Printf("[%s] %s \"%s %s %s\" %d %s\n",
			ts.Format("2006-01-02 15:04:05"),
			clientIP(r),
			r.Method,
			r.URL.RequestURI(),
			r.Proto,
			rec.status,
			elapsed.Round(time.Millisecond),
		)
	})
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
	}
	if real := r.Header.Get("X-Real-Ip"); real != "" {
		return real
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func corsMiddleware(next http.Handler) http.Handler {
	const defaultHeaders = "Origin, Content-Type, Accept, Authorization"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS, POST, PUT, DELETE, PATCH")
		// Allow whatever headers a preflight asks for (Range, X-Requested-With,
		// ...) so custom request headers do not fail CORS.
		if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
			h.Set("Access-Control-Allow-Headers", req)
			h.Add("Vary", "Access-Control-Request-Headers")
		} else {
			h.Set("Access-Control-Allow-Headers", defaultHeaders)
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cacheMiddleware(next http.Handler, cfg *Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case cfg.Cache < 0:
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		default:
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cfg.Cache))
		}
		next.ServeHTTP(w, r)
	})
}

func basicAuthMiddleware(next http.Handler, username, password string) http.Handler {
	const realm = `Basic realm="goblet"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		// Compare both fields unconditionally so timing does not reveal
		// whether the username alone was correct.
		userOK := subtle.ConstantTimeCompare([]byte(u), []byte(username))
		passOK := subtle.ConstantTimeCompare([]byte(p), []byte(password))
		if !ok || userOK&passOK != 1 {
			w.Header().Set("WWW-Authenticate", realm)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type fileHandler struct {
	cfg *Config
}

// matchesExclude reports whether name matches any of the glob patterns.
// Each pattern is matched against the bare name using path.Match semantics.
func matchesExclude(name string, patterns []string) bool {
	for _, pat := range patterns {
		if matched, err := path.Match(pat, name); err == nil && matched {
			return true
		}
	}
	return false
}

// isHidden reports whether a single path component is hidden by --no-dotfiles
// or --exclude.
func isHidden(cfg *Config, name string) bool {
	return (cfg.NoDotfiles && strings.HasPrefix(name, ".")) || matchesExclude(name, cfg.Exclude)
}

// isNotFound reports whether err from a path lookup means the path cannot
// exist: a missing entry, a file used as a parent directory, or a name the
// filesystem rejects.
func isNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, syscall.ENOTDIR) ||
		errors.Is(err, syscall.ENAMETOOLONG) ||
		errors.Is(err, syscall.EINVAL)
}

// fileInfo checks every component, since Lstat on the final path alone would
// follow symlinks in parent directories. It also applies the visibility rules
// to fallback files such as about.html and index.html.
func fileInfo(cfg *Config, fsPath string) (os.FileInfo, bool, error) {
	rel, err := filepath.Rel(cfg.Root, fsPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, true, nil
	}
	if rel == "." {
		fi, err := os.Stat(cfg.Root)
		return fi, false, err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := cfg.Root
	var fi os.FileInfo
	for i, part := range parts {
		if isHidden(cfg, part) {
			return nil, true, nil
		}
		current = filepath.Join(current, part)
		fi, err = os.Lstat(current)
		if err != nil {
			return nil, false, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if !cfg.Symlinks {
				return nil, true, nil
			}
			fi, err = os.Stat(current)
			if err != nil {
				return nil, false, err
			}
		}
		if cfg.NoDirs && i < len(parts)-1 && fi.IsDir() {
			return nil, true, nil
		}
	}
	return fi, false, nil
}

// servableFile returns the FileInfo for p if it is a visible regular file.
func servableFile(cfg *Config, p string) (os.FileInfo, bool) {
	fi, denied, err := fileInfo(cfg, p)
	if denied || err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	return fi, true
}

func (fh *fileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// path.Clean resolves ".." so callers cannot escape the root.
	urlPath := path.Clean("/" + r.URL.Path)

	fsPath := filepath.Join(fh.cfg.Root, filepath.FromSlash(strings.TrimPrefix(urlPath, "/")))
	fi, denied, err := fileInfo(fh.cfg, fsPath)
	// Hidden paths get exactly the same response as missing ones, so the
	// custom 404 page or SPA fallback cannot reveal that they exist.
	if denied || isNotFound(err) {
		fh.notFound(w, r, fsPath)
		return
	}
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if fi.IsDir() {
		// --no-dirs blocks subdirectory access but leaves the root listing
		// intact so users still get a flat file index.
		if fh.cfg.NoDirs && urlPath != "/" {
			fh.notFound(w, r, fsPath)
			return
		}
		// Redirect bare directory paths to their trailing-slash form so that
		// relative links in served HTML resolve correctly. The target is built
		// from the cleaned path: the raw one could start with "//", which
		// browsers treat as another host.
		if !strings.HasSuffix(r.URL.Path, "/") {
			target := url.URL{Path: strings.TrimSuffix(urlPath, "/") + "/", RawQuery: r.URL.RawQuery}
			http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
			return
		}
		// Prefer index.html over directory listing.
		idx := filepath.Join(fsPath, "index.html")
		if idxFi, ok := servableFile(fh.cfg, idx); ok {
			serveContent(w, r, idx, idxFi)
			return
		}
		if fh.cfg.NoListing {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		serveDirectoryListing(w, fsPath, urlPath, fh.cfg)
		return
	}

	// Only regular files are served: opening a FIFO or device would block
	// the request forever.
	if !fi.Mode().IsRegular() {
		fh.notFound(w, r, fsPath)
		return
	}
	serveContent(w, r, fsPath, fi)
}

// notFound tries the fallbacks for a path that does not resolve to anything
// servable: the default extension, the SPA index, then a custom 404 page.
func (fh *fileHandler) notFound(w http.ResponseWriter, r *http.Request, fsPath string) {
	// Try appending the default extension (e.g. /about → /about.html).
	if ext := strings.TrimPrefix(fh.cfg.Ext, "."); ext != "" {
		candidate := fsPath + "." + ext
		if ci, ok := servableFile(fh.cfg, candidate); ok {
			serveContent(w, r, candidate, ci)
			return
		}
	}
	// SPA fallback: serve root index.html so client-side routing works.
	if fh.cfg.SPA {
		idx := filepath.Join(fh.cfg.Root, "index.html")
		if idxFi, ok := servableFile(fh.cfg, idx); ok {
			serveContent(w, r, idx, idxFi)
			return
		}
	}
	// Serve a custom 404 page if present in the root.
	p404 := filepath.Join(fh.cfg.Root, "404.html")
	if _, ok := servableFile(fh.cfg, p404); ok {
		if content, err := os.ReadFile(p404); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write(content) //nolint:errcheck
			return
		}
	}
	http.NotFound(w, r)
}

// serveContent opens fsPath and delegates to http.ServeContent, which handles
// Range requests (partial content / media seeking) and conditional GET via
// Last-Modified. fi must be the FileInfo for fsPath.
func serveContent(w http.ResponseWriter, r *http.Request, fsPath string, fi os.FileInfo) {
	f, err := os.Open(fsPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

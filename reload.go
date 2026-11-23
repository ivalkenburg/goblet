package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const reloadSSEPath = "/_goblet/reload"

// liveReloadScript is injected into HTML responses when --watch is active.
// It opens an SSE connection and reloads the page on file changes or
// when the server comes back up after a restart.
const liveReloadScript = `(function(){
  var es=new EventSource('` + reloadSSEPath + `');
  var up=false;
  es.onopen=function(){if(up)location.reload();up=true;};
  es.onmessage=function(e){if(e.data==='reload')location.reload();};
})();`

// reloader watches the filesystem and pushes reload signals to SSE clients.
type reloader struct {
	mu       sync.Mutex
	clients  map[chan struct{}]struct{}
	watcher  *fsnotify.Watcher
	done     chan struct{} // closed by shutdown() to unblock all SSE handlers
	stopOnce sync.Once
	exclude  []string
}

// newReloader starts an fsnotify watcher on root (recursively) and returns
// a reloader that broadcasts to all SSE clients when files change.
// Directories whose names match any of the exclude patterns are skipped.
func newReloader(root string, exclude []string) (*reloader, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("live reload: %w", err)
	}

	// Add the root and every subdirectory so nested changes are caught.
	// Skip directories that match an exclude pattern (e.g. node_modules).
	if err := watchTree(w, root, exclude); err != nil {
		w.Close()
		return nil, fmt.Errorf("live reload watch: %w", err)
	}

	r := &reloader{
		clients: make(map[chan struct{}]struct{}),
		watcher: w,
		done:    make(chan struct{}),
		exclude: exclude,
	}
	go r.watch()
	return r, nil
}

func watchTree(w *fsnotify.Watcher, root string, exclude []string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && matchesExclude(d.Name(), exclude) {
			return fs.SkipDir
		}
		return w.Add(path)
	})
}

func (r *reloader) watch() {
	w := r.watcher
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if matchesExclude(filepath.Base(event.Name), r.exclude) {
				continue
			}
			// Watch newly created directories so they are covered too.
			// Use Lstat to match watchTree and avoid following symlinked dirs.
			if event.Has(fsnotify.Create) {
				if fi, err := os.Lstat(event.Name); err == nil && fi.IsDir() {
					_ = watchTree(w, event.Name, r.exclude)
				}
			}
			// Only reload on content-changing events; ignore Chmod and reads.
			const mutating = fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename
			if !event.Has(mutating) {
				continue
			}
			// Debounce: editors often emit several events per save.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(time.Second, r.broadcast)
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
		}
	}
}

// shutdown unblocks all active SSE handlers and stops the file watcher.
func (r *reloader) shutdown() {
	r.stopOnce.Do(func() {
		close(r.done)
		r.watcher.Close()
	})
}

func (r *reloader) broadcast() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.clients {
		select {
		case ch <- struct{}{}:
		default: // client is slow; skip rather than block
		}
	}
}

// ServeHTTP implements the SSE endpoint that browsers connect to.
func (r *reloader) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// The server's normal WriteTimeout is for finite file responses. SSE
	// connections must stay writable across that deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering

	// Register before flushing so a change right after the client sees the
	// connection open is not missed.
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.clients, ch)
		r.mu.Unlock()
	}()

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-r.done:
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "data: reload\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			// Keep the connection alive through proxies.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// liveReloadMiddleware wraps next and injects liveReloadScript before </body>
// in any HTTP 200 text/html response.
func liveReloadMiddleware(next http.Handler) http.Handler {
	tag := []byte("<script>" + liveReloadScript + "</script>")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		iw := &injectWriter{ResponseWriter: w, tag: tag}
		next.ServeHTTP(iw, r)
		iw.finalize()
	})
}

// injectWriter buffers HTML responses so we can append the reload script.
type injectWriter struct {
	http.ResponseWriter
	tag     []byte
	buf     bytes.Buffer
	inject  bool
	started bool
}

func (w *injectWriter) WriteHeader(code int) {
	if w.started {
		return
	}
	w.started = true
	if code == http.StatusOK {
		ct := w.ResponseWriter.Header().Get("Content-Type")
		if len(ct) >= len("text/html") && strings.EqualFold(ct[:len("text/html")], "text/html") {
			w.inject = true
			// Remove Content-Length; it will be wrong after injection.
			w.ResponseWriter.Header().Del("Content-Length")
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *injectWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if w.inject {
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush forwards flushes so streaming responses work through the writer.
func (w *injectWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *injectWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// finalize writes the (possibly modified) buffer to the underlying writer.
func (w *injectWriter) finalize() {
	if !w.inject {
		return
	}
	body := w.buf.Bytes()
	if i := lastIndexFold(body, "</body>"); i >= 0 {
		_, _ = w.ResponseWriter.Write(body[:i])
		_, _ = w.ResponseWriter.Write(w.tag)
		_, _ = w.ResponseWriter.Write(body[i:])
	} else {
		_, _ = w.ResponseWriter.Write(body)
		_, _ = w.ResponseWriter.Write(w.tag)
	}
}

// lastIndexFold is bytes.LastIndex with ASCII case folding, so "</BODY>" is
// found too. Lowercasing the whole body would allocate and, for non-ASCII
// bytes, could shift offsets.
func lastIndexFold(s []byte, sep string) int {
	if len(sep) == 0 {
		return len(s)
	}
	sepBytes := []byte(sep)
	first := sepBytes[0]
	for i := len(s) - len(sepBytes); i >= 0; i-- {
		if s[i] != first && (s[i]|0x20) != (first|0x20) {
			continue
		}
		if bytes.EqualFold(s[i:i+len(sepBytes)], sepBytes) {
			return i
		}
	}
	return -1
}

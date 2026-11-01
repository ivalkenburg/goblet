package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// injectWriter

func TestInjectWriter_InjectsBeforeBodyTag(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Type", "text/html; charset=utf-8")
	iw := &injectWriter{ResponseWriter: rr, tag: []byte("<script>reload()</script>")}

	iw.WriteHeader(http.StatusOK)
	iw.Write([]byte("<html><body><p>hi</p></body></html>"))
	iw.finalize()

	body := rr.Body.String()
	if !strings.Contains(body, "<script>reload()</script>") {
		t.Fatalf("script tag not injected; body: %s", body)
	}
	scriptIdx := strings.Index(body, "<script>")
	bodyCloseIdx := strings.Index(body, "</body>")
	if scriptIdx > bodyCloseIdx {
		t.Errorf("script injected after </body>; body: %s", body)
	}
}

func TestInjectWriter_InjectsWhenNoBodyTag(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Type", "text/html")
	iw := &injectWriter{ResponseWriter: rr, tag: []byte("<script>reload()</script>")}

	iw.WriteHeader(http.StatusOK)
	iw.Write([]byte("<p>no closing body tag</p>"))
	iw.finalize()

	body := rr.Body.String()
	if !strings.Contains(body, "<script>reload()</script>") {
		t.Fatalf("script not appended when </body> absent; body: %s", body)
	}
}

func TestInjectWriter_PassesThroughNonHTML(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Type", "text/css")
	iw := &injectWriter{ResponseWriter: rr, tag: []byte("<script>reload()</script>")}

	iw.WriteHeader(http.StatusOK)
	iw.Write([]byte("body { color: red; }"))
	iw.finalize()

	body := rr.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("script injected into non-HTML response; body: %s", body)
	}
	if body != "body { color: red; }" {
		t.Errorf("non-HTML body was unexpectedly modified; got: %s", body)
	}
}

func TestInjectWriter_NoInjectionForNon200(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Type", "text/html")
	iw := &injectWriter{ResponseWriter: rr, tag: []byte("<script>reload()</script>")}

	iw.WriteHeader(http.StatusNotFound)
	iw.Write([]byte("<html><body>Not found</body></html>"))
	iw.finalize()

	if strings.Contains(rr.Body.String(), "<script>") {
		t.Errorf("script should not be injected for non-200 status; body: %s", rr.Body.String())
	}
}

func TestInjectWriter_RemovesContentLength(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Type", "text/html")
	rr.Header().Set("Content-Length", "999")
	iw := &injectWriter{ResponseWriter: rr, tag: []byte("<script>x</script>")}

	iw.WriteHeader(http.StatusOK)

	if rr.Header().Get("Content-Length") != "" {
		t.Error("Content-Length should be removed before HTML injection")
	}
}

func TestInjectWriter_ImplicitWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Type", "text/html")
	iw := &injectWriter{ResponseWriter: rr, tag: []byte("<script>x</script>")}

	// Write without calling WriteHeader first — should implicitly trigger 200.
	iw.Write([]byte("<body></body>"))
	iw.finalize()

	if rr.Code != http.StatusOK {
		t.Errorf("expected implicit 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<script>x</script>") {
		t.Error("script not injected after implicit WriteHeader")
	}
}

// liveReloadMiddleware

func TestLiveReloadMiddleware_InjectsIntoHTML(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body>hello</body></html>"))
	})
	handler := liveReloadMiddleware(next)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if !strings.Contains(rr.Body.String(), liveReloadScript) {
		t.Errorf("live reload script not found in HTML response; body: %s", rr.Body.String())
	}
}

func TestLiveReloadMiddleware_SkipsNonHTML(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("body{}"))
	})
	handler := liveReloadMiddleware(next)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/style.css", nil))

	if strings.Contains(rr.Body.String(), "<script>") {
		t.Error("script should not be injected into CSS response")
	}
}

// reloader SSE endpoint

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	called   bool
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.called = true
	return nil
}

func TestReloader_ClearsWriteDeadlineThroughLogger(t *testing.T) {
	r := &reloader{clients: make(map[chan struct{}]struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	r.ServeHTTP(&statusRecorder{ResponseWriter: w}, httptest.NewRequest("GET", reloadSSEPath, nil).WithContext(ctx))
	if !w.called || !w.deadline.IsZero() {
		t.Fatal("SSE did not clear the server write deadline")
	}
}

func TestReloader_SSEHeaders(t *testing.T) {
	r := &reloader{clients: make(map[chan struct{}]struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so ServeHTTP returns right away
	req := httptest.NewRequest("GET", reloadSSEPath, nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestReloader_SSEClientRegisteredAndRemoved(t *testing.T) {
	r := &reloader{clients: make(map[chan struct{}]struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", reloadSSEPath, nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		r.ServeHTTP(rr, req)
		close(done)
	}()

	// Wait until the client is registered.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.clients)
		r.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	r.mu.Lock()
	if len(r.clients) != 1 {
		t.Errorf("expected 1 registered client, got %d", len(r.clients))
	}
	r.mu.Unlock()

	cancel() // disconnect
	<-done

	r.mu.Lock()
	if len(r.clients) != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", len(r.clients))
	}
	r.mu.Unlock()
}

// reloader broadcast

func TestReloader_BroadcastReachesClient(t *testing.T) {
	r := &reloader{clients: make(map[chan struct{}]struct{})}

	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()

	r.broadcast()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Error("broadcast did not deliver to client channel")
	}
}

func TestReloader_BroadcastDropsSlowClient(t *testing.T) {
	r := &reloader{clients: make(map[chan struct{}]struct{})}

	// Unbuffered channel — broadcast must not block.
	ch := make(chan struct{})
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.broadcast()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("broadcast blocked on slow client")
	}
}

// newReloader filesystem watching

func TestNewReloader_WatchesRootDirectory(t *testing.T) {
	dir := newTestDir(t)
	r, err := newReloader(dir, nil)
	if err != nil {
		t.Fatalf("newReloader failed: %v", err)
	}

	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()

	if err := os.WriteFile(filepath.Join(dir, "change.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Error("no broadcast received after file change in root")
	}
}

func TestNewReloader_WatchesSubdirectory(t *testing.T) {
	dir := newTestDir(t)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	r, err := newReloader(dir, nil)
	if err != nil {
		t.Fatalf("newReloader failed: %v", err)
	}

	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()

	if err := os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Error("no broadcast received after change in subdirectory")
	}
}

func TestNewReloader_ExcludedDirNotWatched(t *testing.T) {
	dir := newTestDir(t)
	excluded := filepath.Join(dir, "node_modules")
	if err := os.Mkdir(excluded, 0755); err != nil {
		t.Fatal(err)
	}

	r, err := newReloader(dir, []string{"node_modules"})
	if err != nil {
		t.Fatalf("newReloader failed: %v", err)
	}
	defer r.shutdown()

	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()

	if err := os.WriteFile(filepath.Join(excluded, "change.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
		t.Error("should not broadcast for changes inside an excluded directory")
	case <-time.After(2 * time.Second):
		// expected: no broadcast
	}
}

func TestNewReloader_NonExcludedSubdirStillWatched(t *testing.T) {
	dir := newTestDir(t)
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}

	r, err := newReloader(dir, []string{"node_modules"})
	if err != nil {
		t.Fatalf("newReloader failed: %v", err)
	}
	defer r.shutdown()

	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()

	if err := os.WriteFile(filepath.Join(src, "main.js"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Error("no broadcast received for change in non-excluded directory")
	}
}

func TestNewReloader_WatchesMovedInDirectoryTree(t *testing.T) {
	dir := newTestDir(t)
	staging := newTestDir(t)
	os.MkdirAll(filepath.Join(staging, "top", "nested"), 0755)
	r, err := newReloader(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.shutdown()
	if err := os.Rename(filepath.Join(staging, "top"), filepath.Join(dir, "top")); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "top", "nested")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, watched := range r.watcher.WatchList() {
			if watched == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("new nested directory %q was not watched", want)
}

// printBanner — live reload line

func TestPrintBanner_LiveReloadEnabled(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, Watch: true}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "Live reload") {
		t.Errorf("expected 'Live reload' in banner when Watch=true, got:\n%s", out)
	}
}

func TestPrintBanner_LiveReloadNotShownWhenDisabled(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, Watch: false}
	out := captureBanner(cfg, "http")
	if strings.Contains(out, "Live reload") {
		t.Errorf("expected no 'Live reload' line when Watch=false, got:\n%s", out)
	}
}

// injectWriter — Flush

func TestInjectWriter_FlushForwards(t *testing.T) {
	rr := httptest.NewRecorder()
	(&injectWriter{ResponseWriter: rr}).Flush()
	if !rr.Flushed {
		t.Error("Flush was not forwarded to the underlying writer")
	}
}

// reloader SSE stream

// nonFlusher hides the Flusher of the wrapped recorder.
type nonFlusher struct{ http.ResponseWriter }

func TestReloader_RejectsWriterWithoutFlush(t *testing.T) {
	r := &reloader{clients: make(map[chan struct{}]struct{})}
	rr := httptest.NewRecorder()
	r.ServeHTTP(nonFlusher{rr}, httptest.NewRequest("GET", reloadSSEPath, nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}

func TestReloader_StreamsReloadAndStopsOnShutdown(t *testing.T) {
	r, err := newReloader(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// The client is registered by the time the response headers arrive.
	r.broadcast()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "data: reload\n" {
		t.Errorf("unexpected event %q", line)
	}

	r.shutdown()
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, resp.Body)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("stream stayed open after shutdown")
	}
}

// newReloader — excluded paths

func waitForNoBroadcast(t *testing.T, r *reloader, ch chan struct{}, change func()) {
	t.Helper()
	r.mu.Lock()
	r.clients[ch] = struct{}{}
	r.mu.Unlock()
	change()
	select {
	case <-ch:
		t.Error("change to an excluded path triggered a reload")
	case <-time.After(1500 * time.Millisecond):
	}
}

func TestNewReloader_IgnoresCreatedExcludedDirectory(t *testing.T) {
	dir := t.TempDir()
	r, err := newReloader(dir, []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.shutdown()

	waitForNoBroadcast(t, r, make(chan struct{}, 1), func() {
		os.Mkdir(filepath.Join(dir, "node_modules"), 0755)
		time.Sleep(100 * time.Millisecond)
		os.WriteFile(filepath.Join(dir, "node_modules", "dep.js"), []byte("x"), 0644)
	})
}

func TestNewReloader_DotfilePatternIgnoresDotDirectory(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".git"), 0755)
	r, err := newReloader(dir, watchExcludes(&Config{NoDotfiles: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer r.shutdown()

	waitForNoBroadcast(t, r, make(chan struct{}, 1), func() {
		os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0644)
	})
}

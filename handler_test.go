package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusRecorder

func TestStatusRecorder_DefaultStatus(t *testing.T) {
	rec := &statusRecorder{status: http.StatusOK}
	if rec.status != http.StatusOK {
		t.Errorf("expected default status 200, got %d", rec.status)
	}
}

func TestStatusRecorder_WriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK}
	rec.WriteHeader(http.StatusNotFound)
	if rec.status != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.status)
	}
	if rr.Code != http.StatusNotFound {
		t.Errorf("underlying writer expected 404, got %d", rr.Code)
	}
}

func TestStatusRecorder_Write(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK}
	n, err := rec.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if rec.size != 5 {
		t.Errorf("expected size 5, got %d", rec.size)
	}
}

func TestStatusRecorder_WriteAccumulatesSize(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK}
	rec.Write([]byte("abc"))
	rec.Write([]byte("de"))
	if rec.size != 5 {
		t.Errorf("expected accumulated size 5, got %d", rec.size)
	}
}

// clientIP

func TestClientIP_XForwardedFor_Single(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if ip := clientIP(r); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XForwardedFor_Multiple(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if ip := clientIP(r); ip != "1.2.3.4" {
		t.Errorf("expected first IP 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-Ip", "9.10.11.12")
	if ip := clientIP(r); ip != "9.10.11.12" {
		t.Errorf("expected 9.10.11.12, got %s", ip)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:5000"
	if ip := clientIP(r); ip != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", ip)
	}
}

func TestClientIP_XForwardedTakesPriorityOverXReal(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-Ip", "9.10.11.12")
	if ip := clientIP(r); ip != "1.2.3.4" {
		t.Errorf("X-Forwarded-For should take priority; got %s", ip)
	}
}

// corsMiddleware

func TestCORSMiddleware_SetsHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(next)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected Access-Control-Allow-Origin *, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "GET, HEAD, OPTIONS" {
		t.Errorf("unexpected Access-Control-Allow-Methods: %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected Access-Control-Allow-Headers to be set")
	}
}

func TestCORSMiddleware_OptionsReturns204(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called for OPTIONS preflight")
	})
	handler := corsMiddleware(next)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", rr.Code)
	}
}

func TestCORSMiddleware_PassesThrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(next)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Error("expected next handler to be called for non-OPTIONS request")
	}
}

// cacheMiddleware

func TestCacheMiddleware_NegativeDisablesCache(t *testing.T) {
	cfg := &Config{Cache: -1}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rr := httptest.NewRecorder()
	cacheMiddleware(next, cfg).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if got := rr.Header().Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
		t.Errorf("unexpected Cache-Control: %q", got)
	}
	if got := rr.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("expected Pragma: no-cache, got %q", got)
	}
	if got := rr.Header().Get("Expires"); got != "0" {
		t.Errorf("expected Expires: 0, got %q", got)
	}
}

func TestCacheMiddleware_PositiveSetsMaxAge(t *testing.T) {
	cfg := &Config{Cache: 3600}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rr := httptest.NewRecorder()
	cacheMiddleware(next, cfg).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("unexpected Cache-Control: %q", got)
	}
}

func TestCacheMiddleware_ZeroSetsNoHeader(t *testing.T) {
	cfg := &Config{Cache: 0}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rr := httptest.NewRecorder()
	cacheMiddleware(next, cfg).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if got := rr.Header().Get("Cache-Control"); got != "" {
		t.Errorf("expected no Cache-Control header for cache=0, got %q", got)
	}
}

// basicAuthMiddleware

func TestBasicAuth_ValidCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := basicAuthMiddleware(next, "user", "pass")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "pass")
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid credentials, got %d", rr.Code)
	}
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := basicAuthMiddleware(next, "user", "pass")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "wrong")
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong password, got %d", rr.Code)
	}
}

func TestBasicAuth_WrongUsername(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := basicAuthMiddleware(next, "user", "pass")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "pass")
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong username, got %d", rr.Code)
	}
}

func TestBasicAuth_NoCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := basicAuthMiddleware(next, "user", "pass")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header to be set")
	}
}

// fileHandler helpers

func newTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "goblet-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// fileHandler

func TestFileHandler_ServeFile(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/hello.txt", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hello world") {
		t.Error("expected file content in response body")
	}
}

func TestFileHandler_NotFound(t *testing.T) {
	dir := newTestDir(t)
	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/nonexistent.txt", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestFileHandler_Custom404Page(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "404.html"), []byte("<h1>Custom Not Found</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/missing.txt", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Custom Not Found") {
		t.Errorf("expected custom 404 content, got: %s", rr.Body.String())
	}
}

func TestFileHandler_DirectoryRedirect(t *testing.T) {
	dir := newTestDir(t)
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/subdir", nil))

	if rr.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301 redirect, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/subdir/" {
		t.Errorf("expected redirect to /subdir/, got %q", loc)
	}
}

func TestFileHandler_ServesIndexHTML(t *testing.T) {
	dir := newTestDir(t)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "index.html"), []byte("<h1>Index</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/sub/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Index") {
		t.Errorf("expected index.html content in response")
	}
}

func TestFileHandler_NoListing_ReturnsForbidden(t *testing.T) {
	dir := newTestDir(t)
	fh := &fileHandler{cfg: &Config{Root: dir, NoListing: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestFileHandler_NoDotfiles_DeniesAccess(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, ".secret"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir, NoDotfiles: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/.secret", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for dotfile with NoDotfiles, got %d", rr.Code)
	}
}

func TestFileHandler_NoDotfiles_AllowsNormalFiles(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "public.txt"), []byte("visible"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir, NoDotfiles: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/public.txt", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for normal file with NoDotfiles, got %d", rr.Code)
	}
}

func TestFileHandler_DefaultExtension(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "about.html"), []byte("<h1>About</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir, Ext: "html"}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/about", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "About") {
		t.Error("expected content from about.html")
	}
}

func TestFileHandler_DefaultExtension_NotFound(t *testing.T) {
	dir := newTestDir(t)
	fh := &fileHandler{cfg: &Config{Root: dir, Ext: "html"}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/missing", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 when extension fallback also missing, got %d", rr.Code)
	}
}

func TestFileHandler_PathTraversalBlocked(t *testing.T) {
	dir := newTestDir(t)
	// Create a file outside the served root
	secret, err := os.CreateTemp("", "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	secret.WriteString("top secret")
	secret.Close()
	defer os.Remove(secret.Name())

	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	// Attempt traversal; path.Clean in the handler collapses this to "/"
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/../../etc/passwd", nil))

	// Should either 200 (serves root listing) or 403 — crucially NOT serve the secret file
	body := rr.Body.String()
	if strings.Contains(body, "top secret") {
		t.Error("path traversal: secret content was served")
	}
}

// fileExists

func TestFileExists_ExistingFile(t *testing.T) {
	dir := newTestDir(t)
	p := filepath.Join(dir, "test.txt")
	os.WriteFile(p, []byte("x"), 0644)
	if !fileExists(p) {
		t.Error("expected fileExists to return true for existing file")
	}
}

func TestFileExists_NonExistentFile(t *testing.T) {
	if fileExists("/tmp/goblet-nonexistent-file-xyz") {
		t.Error("expected fileExists to return false for non-existent file")
	}
}

func TestFileExists_Directory(t *testing.T) {
	dir := newTestDir(t)
	if fileExists(dir) {
		t.Error("expected fileExists to return false for a directory")
	}
}

// symlink

func makeSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink (may need elevated privileges): %v", err)
	}
}

func TestFileHandler_Symlink_BlockedByDefault(t *testing.T) {
	dir := newTestDir(t)
	target := filepath.Join(dir, "real.txt")
	os.WriteFile(target, []byte("real content"), 0644)
	link := filepath.Join(dir, "link.txt")
	makeSymlink(t, target, link)

	fh := &fileHandler{cfg: &Config{Root: dir}} // Symlinks: false by default
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/link.txt", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for symlink when following disabled, got %d", rr.Code)
	}
}

func TestFileHandler_Symlink_Followed(t *testing.T) {
	dir := newTestDir(t)
	target := filepath.Join(dir, "real.txt")
	os.WriteFile(target, []byte("real content"), 0644)
	link := filepath.Join(dir, "link.txt")
	makeSymlink(t, target, link)

	fh := &fileHandler{cfg: &Config{Root: dir, Symlinks: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/link.txt", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for symlink when following enabled, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "real content") {
		t.Error("expected symlink target content in response")
	}
}

func TestFileHandler_Symlink_ToDir_Blocked(t *testing.T) {
	dir := newTestDir(t)
	subdir := filepath.Join(dir, "subdir")
	os.Mkdir(subdir, 0755)
	link := filepath.Join(dir, "linkdir")
	makeSymlink(t, subdir, link)

	fh := &fileHandler{cfg: &Config{Root: dir}} // Symlinks: false
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/linkdir", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for symlink-to-dir when following disabled, got %d", rr.Code)
	}
}

func TestFileHandler_Symlink_ToDir_Followed(t *testing.T) {
	dir := newTestDir(t)
	subdir := filepath.Join(dir, "subdir")
	os.Mkdir(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "index.html"), []byte("<h1>Sub</h1>"), 0644)
	link := filepath.Join(dir, "linkdir")
	makeSymlink(t, subdir, link)

	fh := &fileHandler{cfg: &Config{Root: dir, Symlinks: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/linkdir/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for symlink-to-dir when following enabled, got %d", rr.Code)
	}
}

// SPA fallback

func TestFileHandler_SPA_FallbackToIndex(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>App</h1>"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, SPA: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/some/deep/route", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 SPA fallback, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "App") {
		t.Error("expected root index.html content in SPA fallback")
	}
}

func TestFileHandler_SPA_ExistingFileStillServed(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>App</h1>"), 0644)
	os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{}"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, SPA: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/style.css", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for existing file, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "body{}") {
		t.Error("expected css content, not SPA fallback")
	}
}

func TestFileHandler_SPA_NoIndexFile_Returns404(t *testing.T) {
	dir := newTestDir(t)
	// No index.html in root

	fh := &fileHandler{cfg: &Config{Root: dir, SPA: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/missing", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 when SPA enabled but no index.html, got %d", rr.Code)
	}
}

func TestFileHandler_SPA_Disabled_Returns404(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>App</h1>"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, SPA: false}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/missing", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 when SPA disabled, got %d", rr.Code)
	}
}

// matchesExclude

func TestMatchesExclude_ExactName(t *testing.T) {
	if !matchesExclude("node_modules", []string{"node_modules"}) {
		t.Error("expected exact name match")
	}
}

func TestMatchesExclude_GlobPattern(t *testing.T) {
	if !matchesExclude(".env", []string{"*.env", "*.log"}) {
		t.Error("expected glob pattern *.env to match .env")
	}
	if !matchesExclude("server.log", []string{"*.env", "*.log"}) {
		t.Error("expected glob pattern *.log to match server.log")
	}
}

func TestMatchesExclude_NoMatch(t *testing.T) {
	if matchesExclude("style.css", []string{"*.env", "*.log"}) {
		t.Error("expected no match for style.css against *.env / *.log")
	}
}

func TestMatchesExclude_EmptyPatterns(t *testing.T) {
	if matchesExclude("anything", nil) {
		t.Error("expected false with nil patterns")
	}
}

// fileHandler — --exclude

func TestFileHandler_Exclude_BlocksFile(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "secret.env"), []byte("KEY=123"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, Exclude: []string{"*.env"}}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/secret.env", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for excluded file, got %d", rr.Code)
	}
}

func TestFileHandler_Exclude_BlocksDirectory(t *testing.T) {
	dir := newTestDir(t)
	sub := filepath.Join(dir, "node_modules")
	os.Mkdir(sub, 0755)
	os.WriteFile(filepath.Join(sub, "pkg.js"), []byte("module"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, Exclude: []string{"node_modules"}}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/node_modules/pkg.js", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for file inside excluded dir, got %d", rr.Code)
	}
}

func TestFileHandler_Exclude_AllowsNonMatchingFiles(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{}"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, Exclude: []string{"*.env"}}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/style.css", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for non-excluded file, got %d", rr.Code)
	}
}

// fileHandler — --no-dirs

func TestFileHandler_NoDirs_BlocksDirectoryAccess(t *testing.T) {
	dir := newTestDir(t)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	fh := &fileHandler{cfg: &Config{Root: dir, NoDirs: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/subdir/", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for directory with --no-dirs, got %d", rr.Code)
	}
}

func TestFileHandler_NoDirs_BlocksRedirect(t *testing.T) {
	dir := newTestDir(t)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	fh := &fileHandler{cfg: &Config{Root: dir, NoDirs: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/subdir", nil))

	// Without --no-dirs this would be a 301 redirect; with it, 404.
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for directory path without trailing slash with --no-dirs, got %d", rr.Code)
	}
}

func TestFileHandler_NoDirs_AllowsFiles(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, NoDirs: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/readme.txt", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for file with --no-dirs, got %d", rr.Code)
	}
}

func TestFileHandler_NoDirs_BlocksDirWithIndexHTML(t *testing.T) {
	dir := newTestDir(t)
	sub := filepath.Join(dir, "subdir")
	os.Mkdir(sub, 0755)
	os.WriteFile(filepath.Join(sub, "index.html"), []byte("<h1>hi</h1>"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, NoDirs: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/subdir/", nil))

	// The directory has an index.html but --no-dirs blocks before index fallback.
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for dir-with-index.html with --no-dirs, got %d", rr.Code)
	}
}

func TestFileHandler_NoDirs_RootStillAccessible(t *testing.T) {
	dir := newTestDir(t)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644)

	fh := &fileHandler{cfg: &Config{Root: dir, NoDirs: true}}
	rr := httptest.NewRecorder()
	fh.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected root listing to remain accessible with --no-dirs, got %d", rr.Code)
	}
}

// Range requests and conditional GET

func TestFileHandler_RangeRequest(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/data.txt", nil)
	req.Header.Set("Range", "bytes=0-4")
	fh.ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Errorf("expected 206 Partial Content, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "hello" {
		t.Errorf("expected partial body %q, got %q", "hello", body)
	}
	if rr.Header().Get("Content-Range") == "" {
		t.Error("expected Content-Range header to be set")
	}
}

func TestFileHandler_ConditionalGet_NotModified(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	fh := &fileHandler{cfg: &Config{Root: dir}}

	// First request to obtain Last-Modified.
	rr1 := httptest.NewRecorder()
	fh.ServeHTTP(rr1, httptest.NewRequest("GET", "/data.txt", nil))
	lastModified := rr1.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("expected Last-Modified header in first response")
	}

	// Conditional request should return 304.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/data.txt", nil)
	req2.Header.Set("If-Modified-Since", lastModified)
	fh.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusNotModified {
		t.Errorf("expected 304 Not Modified, got %d", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Error("expected empty body for 304 response")
	}
}

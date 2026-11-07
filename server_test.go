package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Start — validation errors

func TestStart_RootNotExist(t *testing.T) {
	err := Start(&Config{Root: "/tmp/goblet-nonexistent-dir-xyz"})
	if err == nil || !strings.Contains(err.Error(), "cannot access") {
		t.Errorf("expected 'cannot access' error, got %v", err)
	}
}

func TestStart_RootIsFile(t *testing.T) {
	f, err := os.CreateTemp("", "goblet-file-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	err = Start(&Config{Root: f.Name()})
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("expected 'is not a directory' error, got %v", err)
	}
}

func TestStart_UsernameWithoutPassword(t *testing.T) {
	dir := newTestDir(t)
	err := Start(&Config{Root: dir, Username: "user"})
	if err == nil || !strings.Contains(err.Error(), "must both be provided") {
		t.Errorf("expected auth pairing error, got %v", err)
	}
}

func TestStart_PasswordWithoutUsername(t *testing.T) {
	dir := newTestDir(t)
	err := Start(&Config{Root: dir, Password: "pass"})
	if err == nil || !strings.Contains(err.Error(), "must both be provided") {
		t.Errorf("expected auth pairing error, got %v", err)
	}
}

func TestStart_PortAlreadyInUse(t *testing.T) {
	// Bind a listener to occupy the port first.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	dir := newTestDir(t)
	err = Start(&Config{Root: dir, Port: port, Address: "127.0.0.1", Silent: true})
	if err == nil || !strings.Contains(err.Error(), "cannot listen") {
		t.Errorf("expected 'cannot listen' error, got %v", err)
	}
}

// Start — lifecycle

func TestStart_ServesAndShutsDown(t *testing.T) {
	dir := newTestDir(t)
	if err := os.WriteFile(dir+"/hello.txt", []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use port 0 so the OS picks a free port, but we need to know the port
	// before Start binds it. Instead, pre-find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // release so Start can bind it

	cfg := &Config{Root: dir, Port: port, Address: "127.0.0.1", Silent: true}
	startErr := make(chan error, 1)
	go func() { startErr <- Start(cfg) }()

	// Wait until the server is accepting connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/hello.txt")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Trigger graceful shutdown via SIGTERM.
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGTERM)

	select {
	case err := <-startErr:
		if err != nil {
			t.Errorf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Start did not return after SIGTERM")
	}
}

func TestStart_RandomPort_Binds(t *testing.T) {
	dir := newTestDir(t)
	cfg := &Config{Root: dir, Port: 0, Address: "127.0.0.1", Silent: true}
	startErr := make(chan error, 1)
	go func() { startErr <- Start(cfg) }()

	// Verify the server is still running after a short wait (no early error).
	select {
	case err := <-startErr:
		t.Fatalf("Start returned early with error: %v", err)
	case <-time.After(100 * time.Millisecond):
		// still running — good
	}

	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGTERM)

	select {
	case err := <-startErr:
		if err != nil {
			t.Errorf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Start did not return after SIGTERM")
	}
}

// localAddresses

func TestLocalAddresses_ReturnsIPv4(t *testing.T) {
	addrs, err := localAddresses()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Errorf("invalid IP returned: %q", addr)
			continue
		}
		if ip.To4() == nil {
			t.Errorf("expected IPv4, got %q", addr)
		}
		if ip.IsLoopback() {
			t.Errorf("loopback address should be excluded: %q", addr)
		}
	}
}

// printBanner

func captureBanner(cfg *Config, scheme string) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printBanner(cfg, scheme, cfg.Port)
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestPrintBanner_ContainsRoot(t *testing.T) {
	cfg := &Config{Root: "/srv/www", Port: 8080}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "/srv/www") {
		t.Errorf("banner should contain root path, got:\n%s", out)
	}
}

func TestPrintBanner_ContainsPort(t *testing.T) {
	cfg := &Config{Root: ".", Port: 9090}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "9090") {
		t.Errorf("banner should contain port number, got:\n%s", out)
	}
}

func TestPrintBanner_UsesBoundAddress(t *testing.T) {
	cfg := &Config{Root: ".", Port: 9090, Address: "::1"}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "http://[::1]:9090") {
		t.Fatalf("banner should show the bound IPv6 address, got:\n%s", out)
	}
}

func TestPrintBanner_GzipDisabled(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, NoGzip: true}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected 'disabled' for gzip, got:\n%s", out)
	}
}

func TestPrintBanner_CacheMaxAge(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, Cache: 3600}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "3600s max-age") {
		t.Errorf("expected cache max-age in banner, got:\n%s", out)
	}
}

func TestPrintBanner_CacheDisabled(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, Cache: -1}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected cache 'disabled' in banner, got:\n%s", out)
	}
}

func TestPrintBanner_AuthBasic(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, Username: "admin", Password: "secret"}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "basic") {
		t.Errorf("expected 'basic' auth in banner, got:\n%s", out)
	}
}

func TestPrintBanner_AuthNone(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "none") {
		t.Errorf("expected 'none' auth in banner, got:\n%s", out)
	}
}

func TestPrintBanner_CORSEnabled(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, CORS: true}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "enabled") {
		t.Errorf("expected CORS 'enabled' in banner, got:\n%s", out)
	}
}

func TestPrintBanner_TLSShowsCertAndKey(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8443, TLS: true, Cert: "cert.pem", Key: "key.pem"}
	out := captureBanner(cfg, "https")
	if !strings.Contains(out, "cert.pem") || !strings.Contains(out, "key.pem") {
		t.Errorf("expected TLS cert/key in banner, got:\n%s", out)
	}
}

func TestPrintBanner_NoListingDisabled(t *testing.T) {
	cfg := &Config{Root: ".", Port: 8080, NoListing: true}
	out := captureBanner(cfg, "http")
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected listing 'disabled' in banner, got:\n%s", out)
	}
}

func TestPrintBanner_ValuesAligned(t *testing.T) {
	out := captureBanner(&Config{Root: ".", Port: 8080, Watch: true, TLS: true, Cert: "c", Key: "k"}, "https")
	col := -1
	for _, label := range []string{"Gzip:", "Cache:", "Listing:", "Auth:", "CORS:", "TLS:", "Live reload:"} {
		_, rest, ok := strings.Cut(out, "  "+label)
		if !ok {
			t.Fatalf("banner missing %q:\n%s", label, out)
		}
		c := len(label) + len(rest) - len(strings.TrimLeft(rest, " "))
		if col != -1 && c != col {
			t.Errorf("%q value at column %d, want %d", label, c, col)
		}
		col = c
	}
}

// browserHost

func TestBrowserHost(t *testing.T) {
	for in, want := range map[string]string{
		"":          "127.0.0.1",
		"0.0.0.0":   "127.0.0.1",
		"::":        "::1",
		"127.0.0.1": "127.0.0.1",
		"::1":       "::1",
		"myhost":    "myhost",
	} {
		if got := browserHost(in); got != want {
			t.Errorf("browserHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// watchExcludes

func TestWatchExcludes(t *testing.T) {
	cfg := &Config{Exclude: []string{"node_modules"}}
	if got := watchExcludes(cfg); !slices.Equal(got, cfg.Exclude) {
		t.Errorf("unexpected patterns: %v", got)
	}

	cfg.NoDotfiles = true
	got := watchExcludes(cfg)
	if !slices.Equal(got, []string{"node_modules", ".*"}) {
		t.Errorf("dotfiles should be excluded: %v", got)
	}
	if len(cfg.Exclude) != 1 {
		t.Errorf("config was mutated: %v", cfg.Exclude)
	}
}

// Start — validation before binding

func TestStart_InvalidExcludePattern(t *testing.T) {
	err := Start(&Config{Root: t.TempDir(), Port: 0, Exclude: []string{"["}})
	if err == nil || !strings.Contains(err.Error(), "--exclude") {
		t.Fatalf("expected --exclude error, got %v", err)
	}
}

func TestStart_MissingTLSCertificateFailsBeforeBanner(t *testing.T) {
	dir := t.TempDir()
	var err error
	out := captureStdout(t, func() {
		err = Start(&Config{Root: dir, Port: 0, TLS: true, Cert: filepath.Join(dir, "no.pem"), Key: filepath.Join(dir, "no.pem")})
	})
	if err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("expected TLS error, got %v", err)
	}
	if out != "" {
		t.Errorf("banner printed before certificate validation: %q", out)
	}
}

// Start — live server behaviour

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func writeSelfSignedCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestStart_ServesTLS(t *testing.T) {
	root, certDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := writeSelfSignedCert(t, certDir)
	port := freePort(t)

	startErr := make(chan error, 1)
	go func() {
		startErr <- Start(&Config{Root: root, Port: port, Address: "127.0.0.1", Silent: true, TLS: true, Cert: certFile, Key: keyFile})
	}()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	var resp *http.Response
	var err error
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if resp, err = client.Get("https://127.0.0.1:" + strconv.Itoa(port) + "/hello.txt"); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("HTTPS request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hi" {
		t.Errorf("unexpected body %q", body)
	}

	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGTERM)
	select {
	case err := <-startErr:
		if err != nil {
			t.Errorf("expected clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after SIGTERM")
	}
}

// run

func TestRun_UsesPathArgument(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	err := run(rootCmd, []string{missing})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected error naming the path, got %v", err)
	}
}

func TestPrintBanner_IPv6AnyListsLocalAddresses(t *testing.T) {
	addrs, err := localAddresses()
	if err != nil || len(addrs) == 0 {
		t.Skip("no non-loopback IPv4 address")
	}
	out := captureBanner(&Config{Root: ".", Port: 9090, Address: "::"}, "http")
	if !strings.Contains(out, "http://"+addrs[0]+":9090") {
		t.Errorf("banner for :: should list LAN addresses, got:\n%s", out)
	}
}

func TestStart_WatchInjectsScriptAndServesSSE(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><body>hi</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	startErr := make(chan error, 1)
	go func() {
		startErr <- Start(&Config{Root: root, Port: port, Address: "127.0.0.1", Silent: true, Watch: true, Cache: -1})
	}()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	var resp *http.Response
	var err error
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if resp, err = http.Get(base + "/"); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), reloadSSEPath) {
		t.Errorf("reload script not injected: %q", body)
	}

	sse, err := http.Get(base + reloadSSEPath)
	if err != nil {
		t.Fatal(err)
	}
	if ct := sse.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("unexpected SSE Content-Type %q", ct)
	}

	// Shutdown must close open SSE streams instead of waiting out the drain.
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGTERM)
	select {
	case err := <-startErr:
		if err != nil {
			t.Errorf("expected clean shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after SIGTERM with an open SSE stream")
	}
	sse.Body.Close()
}

func TestRun_DefaultsToCurrentDirectory(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg.Username = "only-user" // fails validation right after the root check
	err := run(rootCmd, nil)
	if cfg.Root != "." {
		t.Errorf("expected root '.', got %q", cfg.Root)
	}
	if err == nil || !strings.Contains(err.Error(), "--username") {
		t.Errorf("expected credential validation error, got %v", err)
	}
}

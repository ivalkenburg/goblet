package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
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

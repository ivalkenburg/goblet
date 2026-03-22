package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// Start validates config, binds a TCP listener, starts the HTTP server,
// and blocks until an OS interrupt or a fatal error occurs.
func Start(cfg *Config) error {
	info, err := os.Stat(cfg.Root)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", cfg.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", cfg.Root)
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return fmt.Errorf("--username and --password must both be provided together")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Address, cfg.Port))
	if err != nil {
		return fmt.Errorf("cannot listen on port %d: %w", cfg.Port, err)
	}
	// Resolve the actual port — important when port 0 was given (OS picks a
	// free port). Use a local variable to avoid mutating the caller's config.
	port := ln.Addr().(*net.TCPAddr).Port

	scheme := "http"
	if cfg.TLS {
		scheme = "https"
	}

	var r *reloader
	if cfg.Watch {
		var werr error
		r, werr = newReloader(cfg.Root)
		if werr != nil {
			fmt.Fprintf(os.Stderr, "warning: live reload disabled: %v\n", werr)
			cfg.Watch = false
		}
	}

	if !cfg.Silent {
		printBanner(cfg, scheme, port)
	}

	srv := &http.Server{Handler: buildHandler(cfg, r)}
	if cfg.Timeout > 0 {
		d := time.Duration(cfg.Timeout) * time.Second
		srv.ReadHeaderTimeout = d
		srv.ReadTimeout = d
		srv.WriteTimeout = d
		srv.IdleTimeout = d * 2
	}

	if cfg.OpenBrowser {
		go openBrowser(fmt.Sprintf("%s://127.0.0.1:%d", scheme, port))
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serveErr := make(chan error, 1)
	go func() {
		if cfg.TLS {
			serveErr <- srv.ServeTLS(ln, cfg.Cert, cfg.Key)
		} else {
			serveErr <- srv.Serve(ln)
		}
	}()

	select {
	case err := <-serveErr:
		if err != http.ErrServerClosed {
			return err
		}
	case <-quit:
		if !cfg.Silent {
			fmt.Println("\nShutting down...")
		}
		if r != nil {
			r.shutdown()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
	return nil
}

func onOff(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func printBanner(cfg *Config, scheme string, port int) {
	fmt.Printf("\ngoblet v%s — serving %q\n\n", version, cfg.Root)
	fmt.Printf("  %s://127.0.0.1:%d\n", scheme, port)
	if cfg.Address == "" {
		if addrs, err := localAddresses(); err == nil {
			for _, addr := range addrs {
				fmt.Printf("  %s://%s:%d\n", scheme, addr, port)
			}
		}
	}
	fmt.Println()

	cacheVal := "disabled"
	if cfg.Cache >= 0 {
		cacheVal = fmt.Sprintf("%ds max-age", cfg.Cache)
	}
	authVal := "none"
	if cfg.Username != "" {
		authVal = "basic"
	}
	flags := []struct{ label, val string }{
		{"Gzip", onOff(!cfg.NoGzip)},
		{"Cache", cacheVal},
		{"Listing", onOff(!cfg.NoListing)},
		{"Auth", authVal},
		{"CORS", onOff(cfg.CORS)},
	}
	for _, f := range flags {
		fmt.Printf("  %-10s %s\n", f.label+":", f.val)
	}
	if cfg.TLS {
		fmt.Printf("  %-10s %s / %s\n", "TLS:", cfg.Cert, cfg.Key)
	}
	if cfg.Watch {
		fmt.Printf("  %-10s %s\n", "Live reload:", "enabled")
	}
	fmt.Print("\n  Hit CTRL-C to stop\n\n")
}

// localAddresses returns non-loopback IPv4 addresses of the host.
func localAddresses() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var addrs []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifAddrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				addrs = append(addrs, ip4.String())
			}
		}
	}
	return addrs, nil
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}

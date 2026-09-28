package main

import (
	"cmp"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed listing.html
var listingHTML string

var listingTmpl = template.Must(template.New("listing").Parse(listingHTML))

type breadcrumb struct {
	Name   string
	Href   string
	IsLast bool
}

type dirEntry struct {
	Name      string
	Href      string
	IsDir     bool
	IsSymlink bool
	Ext       string
	Size      string
	SizeBytes int64
	ModTime   string
}

type listingData struct {
	Path        string
	Breadcrumbs []breadcrumb
	Entries     []dirEntry
	Version     string
}

func buildBreadcrumbs(urlPath string) []breadcrumb {
	if urlPath == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	crumbs := make([]breadcrumb, 0, len(parts))
	var href strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		href.WriteString("/" + url.PathEscape(p))
		crumbs = append(crumbs, breadcrumb{Name: p, Href: href.String() + "/"})
	}
	if len(crumbs) > 0 {
		crumbs[len(crumbs)-1].IsLast = true
	}
	return crumbs
}

func serveDirectoryListing(w http.ResponseWriter, _ *http.Request, dir, urlPath string, cfg *Config) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}

	var dirs, files []dirEntry
	for _, e := range entries {
		if cfg.NoDotfiles && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if matchesExclude(e.Name(), cfg.Exclude) {
			continue
		}
		isSymlink := e.Type()&os.ModeSymlink != 0
		// Hide symlinks when the server is not configured to follow them.
		if isSymlink && !cfg.Symlinks {
			continue
		}
		var fi os.FileInfo
		if isSymlink {
			fi, err = os.Stat(filepath.Join(dir, e.Name()))
		} else {
			fi, err = e.Info()
		}
		if err != nil || (cfg.NoDirs && fi.IsDir()) {
			continue
		}
		modTime := fi.ModTime()
		if cfg.UTC {
			modTime = modTime.UTC()
		}
		de := dirEntry{
			Name:      e.Name(),
			Href:      url.PathEscape(e.Name()),
			IsDir:     fi.IsDir(),
			IsSymlink: isSymlink,
			ModTime:   modTime.Format("2006-01-02 15:04"),
		}
		if fi.IsDir() {
			if cfg.DirSize {
				n := dirTotalSize(filepath.Join(dir, e.Name()), cfg.Symlinks)
				de.SizeBytes = n
				de.Size = humanSize(n)
			}
		} else {
			de.SizeBytes = fi.Size()
			de.Size = humanSize(fi.Size())
			if i := strings.LastIndex(e.Name(), "."); i > 0 {
				de.Ext = e.Name()[i+1:]
			}
		}
		if fi.IsDir() {
			dirs = append(dirs, de)
		} else {
			files = append(files, de)
		}
	}

	slices.SortFunc(dirs, func(a, b dirEntry) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(files, func(a, b dirEntry) int { return cmp.Compare(a.Name, b.Name) })

	data := listingData{
		Path:        urlPath,
		Breadcrumbs: buildBreadcrumbs(urlPath),
		Entries:     append(dirs, files...),
		Version:     version,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := listingTmpl.Execute(w, data); err != nil {
		fmt.Fprintf(os.Stderr, "listing template error: %v\n", err)
	}
}

func dirTotalSize(dir string, symlinks bool) int64 {
	var total int64
	seen := make(map[string]bool)
	var walk func(string)
	walk = func(path string) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := filepath.Join(path, e.Name())
			if e.Type()&os.ModeSymlink != 0 && !symlinks {
				continue
			}
			fi, err := os.Stat(name)
			if err != nil {
				continue
			}
			if fi.IsDir() {
				walk(name)
			} else {
				total += fi.Size()
			}
		}
	}
	walk(dir)
	return total
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

package main

import (
	_ "embed"
	"cmp"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
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
	href := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		href += "/" + p
		crumbs = append(crumbs, breadcrumb{Name: p, Href: href + "/"})
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
		if cfg.NoDirs && e.IsDir() {
			continue
		}
		if matchesExclude(e.Name(), cfg.Exclude) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		isSymlink := e.Type()&os.ModeSymlink != 0
		// Hide symlinks when the server is not configured to follow them.
		if isSymlink && !cfg.Symlinks {
			continue
		}
		modTime := fi.ModTime()
		if cfg.UTC {
			modTime = modTime.UTC()
		}
		de := dirEntry{
			Name:      e.Name(),
			IsDir:     e.IsDir(),
			IsSymlink: isSymlink,
			ModTime:   modTime.Format("2006-01-02 15:04"),
		}
		if e.IsDir() {
			if cfg.DirSize {
				n := dirTotalSize(filepath.Join(dir, e.Name()))
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
		if e.IsDir() {
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

func dirTotalSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
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

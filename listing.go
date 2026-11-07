package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	for i, p := range parts {
		href.WriteString("/" + url.PathEscape(p))
		crumbs = append(crumbs, breadcrumb{Name: p, Href: href.String() + "/", IsLast: i == len(parts)-1})
	}
	return crumbs
}

// visibleEntry returns the FileInfo for a directory entry, following symlinks
// when they are enabled. It reports false for entries that must stay hidden.
func visibleEntry(cfg *Config, dir string, e os.DirEntry) (os.FileInfo, bool) {
	if isHidden(cfg, e.Name()) {
		return nil, false
	}
	var fi os.FileInfo
	var err error
	if e.Type()&os.ModeSymlink == 0 {
		fi, err = e.Info()
	} else if cfg.Symlinks {
		fi, err = os.Stat(filepath.Join(dir, e.Name()))
	} else {
		return nil, false
	}
	return fi, err == nil
}

func serveDirectoryListing(w http.ResponseWriter, dir, urlPath string, cfg *Config) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}

	// os.ReadDir sorts by name, so dirs and files stay sorted.
	var dirs, files []dirEntry
	for _, e := range entries {
		fi, ok := visibleEntry(cfg, dir, e)
		if !ok || (cfg.NoDirs && fi.IsDir()) {
			continue
		}
		modTime := fi.ModTime()
		if cfg.UTC {
			modTime = modTime.UTC()
		}
		// url.URL prefixes "./" when a colon would otherwise read as a scheme.
		href := url.URL{Path: e.Name()}
		de := dirEntry{
			Name:      e.Name(),
			Href:      href.String(),
			IsDir:     fi.IsDir(),
			IsSymlink: e.Type()&os.ModeSymlink != 0,
			ModTime:   modTime.Format("2006-01-02 15:04"),
		}
		if fi.IsDir() {
			if cfg.DirSize {
				n := dirTotalSize(filepath.Join(dir, e.Name()), cfg)
				de.SizeBytes = n
				de.Size = humanSize(n)
			}
			dirs = append(dirs, de)
		} else {
			de.SizeBytes = fi.Size()
			de.Size = humanSize(fi.Size())
			if i := strings.LastIndex(e.Name(), "."); i > 0 {
				de.Ext = e.Name()[i+1:]
			}
			files = append(files, de)
		}
	}

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

// dirTotalSize sums the sizes of the files under dir that the listing would
// show, so hidden entries neither count nor get walked.
func dirTotalSize(dir string, cfg *Config) int64 {
	var total int64
	seen := make(map[string]bool)
	var walk func(string)
	walk = func(path string) {
		// Cycles are only possible through followed symlinks, so skip the
		// per-directory EvalSymlinks otherwise.
		if cfg.Symlinks {
			real, err := filepath.EvalSymlinks(path)
			if err != nil || seen[real] {
				return
			}
			seen[real] = true
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, e := range entries {
			fi, ok := visibleEntry(cfg, path, e)
			if !ok {
				continue
			}
			if fi.IsDir() {
				walk(filepath.Join(path, e.Name()))
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

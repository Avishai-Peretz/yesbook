package web

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SPA serves a Vite production build: exact static files when present, otherwise
// index.html so client-side routes work. It never serves paths under /api.
type SPA struct {
	root string
}

// NewSPA returns a handler rooted at dir. dir must contain index.html.
func NewSPA(dir string) (*SPA, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, "index.html")); err != nil {
		return nil, err
	}
	return &SPA{root: abs}, nil
}

func (s *SPA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if rel == "" || rel == "." {
		http.ServeFile(w, r, filepath.Join(s.root, "index.html"))
		return
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if !withinRoot(s.root, full) {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(full)
	if err == nil && !fi.IsDir() {
		http.ServeFile(w, r, full)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.root, "index.html"))
}

func withinRoot(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

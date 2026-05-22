package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Handler struct {
	templates *template.Template
}

func NewHandler() (*Handler, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Handler{templates: tmpl}, nil
}

func (h *Handler) Static() http.Handler {
	staticSub, _ := fs.Sub(staticFS, "static")
	return http.FileServer(http.FS(staticSub))
}

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "login.html", nil)
}

func (h *Handler) BrowsePage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "browse.html", nil)
}

func (h *Handler) QueryPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "query.html", nil)
}

func (h *Handler) render(w http.ResponseWriter, tmpl string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, tmpl, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type PageData struct {
	LoggedIn    bool
	AccountEmail string
	Page        string
	Error       string
	Projects    []auth.ProjectCred
}

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
	return http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))
}

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	errStr := r.URL.Query().Get("error")
	data := PageData{Page: "login", Error: errStr}
	h.render(w, "layout.html", data)
}

func (h *Handler) AppPage(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	data := PageData{LoggedIn: true, Page: "app", Projects: session.Projects}
	h.render(w, "layout.html", data)
}

func (h *Handler) ReportsPage(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	data := PageData{LoggedIn: true, Page: "reports", Projects: session.Projects}
	h.render(w, "layout.html", data)
}

func (h *Handler) render(w http.ResponseWriter, tmpl string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, tmpl, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

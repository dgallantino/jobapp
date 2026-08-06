package web

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/activation"

	"jobapp/internal/llm"
	"jobapp/internal/models"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Server is the HTTP frontend.
type Server struct {
	DB           *sql.DB
	PasswordHash string
	SessionSecret string
	LLM          *llm.Client
	templates    map[string]*template.Template
	static       http.Handler
}

// New constructs a Server.
func New(db *sql.DB, passwordHash, sessionSecret string, llmClient *llm.Client) (*Server, error) {
	if sessionSecret == "" {
		return nil, fmt.Errorf("JOBAPP_SESSION_SECRET is required for serve")
	}
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{
		DB:            db,
		PasswordHash:  passwordHash,
		SessionSecret: sessionSecret,
		LLM:           llmClient,
		templates:     map[string]*template.Template{},
		static:        http.FileServer(http.FS(staticFS)),
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) parseTemplates() error {
	type page struct {
		name  string
		files []string
	}
	pages := []page{
		{"login", []string{"templates/layout.html", "templates/login.html"}},
		{"jobs", []string{"templates/layout.html", "templates/jobs.html", "templates/status_cell.html"}},
		{"job_detail", []string{"templates/layout.html", "templates/job_detail.html", "templates/status_cell.html"}},
		{"profile", []string{"templates/layout.html", "templates/profile.html"}},
		{"sources", []string{"templates/layout.html", "templates/sources.html"}},
		{"source_edit", []string{"templates/layout.html", "templates/source_edit.html"}},
		{"status_cell", []string{"templates/status_cell.html"}},
		{"letter_partial", []string{"templates/letter_partial.html"}},
	}
	for _, p := range pages {
		t, err := template.New("").ParseFS(assets, p.files...)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p.name, err)
		}
		s.templates[p.name] = t
	}
	return nil
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t := s.templates[name]
	if t == nil {
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	root := "layout"
	if name == "status_cell" || name == "letter_partial" {
		root = name
	}
	if err := t.ExecuteTemplate(w, root, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

type viewData map[string]any

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)

	authed := http.NewServeMux()
	authed.HandleFunc("GET /{$}", s.handleJobs)
	authed.HandleFunc("GET /jobs/{id}", s.handleJobDetail)
	authed.HandleFunc("PATCH /jobs/{id}/status", s.handleJobStatus)
	authed.HandleFunc("POST /jobs/{id}/cover-letter", s.handleCoverLetter)
	authed.HandleFunc("GET /profile", s.handleProfileGet)
	authed.HandleFunc("POST /profile", s.handleProfilePost)
	authed.HandleFunc("GET /sources", s.handleSourcesGet)
	authed.HandleFunc("POST /sources", s.handleSourcesCreate)
	authed.HandleFunc("GET /sources/{id}/edit", s.handleSourceEditGet)
	authed.HandleFunc("POST /sources/{id}", s.handleSourceUpdate)
	authed.HandleFunc("POST /sources/{id}/delete", s.handleSourceDelete)
	authed.HandleFunc("POST /logout", s.handleLogout)

	mux.Handle("/", s.requireAuth(authed))
	return mux
}

// ListenAndServe starts the server using systemd socket activation if available,
// otherwise listens on listenAddr (e.g. ":8080").
func (s *Server) ListenAndServe(listenAddr string) error {
	handler := s.routes()

	listeners, err := activation.Listeners()
	if err != nil {
		return fmt.Errorf("systemd activation: %w", err)
	}
	if len(listeners) > 0 {
		ln := listeners[0]
		log.Printf("serving (socket-activated) on %s", ln.Addr())
		return http.Serve(ln, handler)
	}

	if listenAddr == "" {
		listenAddr = ":8080"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	log.Printf("serving on http://%s", ln.Addr())
	return http.Serve(ln, handler)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login", viewData{"Authed": false})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !s.checkPassword(r.FormValue("password")) {
		s.render(w, "login", viewData{"Authed": false, "Error": "Invalid password"})
		return
	}
	if err := s.setSession(w); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	jobs, err := models.ListJobAds(r.Context(), s.DB, status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "jobs", viewData{"Authed": true, "Jobs": jobs, "Status": status})
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := models.GetJobAd(r.Context(), s.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	letters, err := models.ListCoverLetters(r.Context(), s.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "job_detail", viewData{"Authed": true, "Job": job, "Letters": letters})
}

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	status := r.FormValue("status")
	if err := models.UpdateJobAdStatus(r.Context(), s.DB, id, status); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	job, err := models.GetJobAd(r.Context(), s.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "status_cell", job)
}

func (s *Server) handleCoverLetter(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := models.GetJobAd(r.Context(), s.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	profile, err := models.ProfileMap(r.Context(), s.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.LLM == nil || s.LLM.APIKey == "" {
		http.Error(w, "OPENROUTER_API_KEY is not configured", http.StatusServiceUnavailable)
		return
	}
	content, err := s.LLM.GenerateCoverLetter(r.Context(), profile, job.Title, job.Company, job.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	letterID, err := models.InsertCoverLetter(r.Context(), s.DB, id, content, s.LLM.Model)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	letter := models.CoverLetter{
		ID:        letterID,
		JobAdID:   id,
		Content:   content,
		Model:     s.LLM.Model,
		CreatedAt: time.Now().UTC(),
	}
	s.render(w, "letter_partial", letter)
}

func (s *Server) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	entries, err := models.ListProfile(r.Context(), s.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "profile", viewData{"Authed": true, "Entries": entries})
}

func (s *Server) handleProfilePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for k := range r.PostForm {
		values[k] = r.FormValue(k)
	}
	if err := models.UpsertProfile(r.Context(), s.DB, values); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	entries, err := models.ListProfile(r.Context(), s.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "profile", viewData{"Authed": true, "Entries": entries, "Saved": true})
}

func (s *Server) handleSourcesGet(w http.ResponseWriter, r *http.Request) {
	sources, err := models.ListSources(r.Context(), s.DB, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "sources", viewData{"Authed": true, "Sources": sources})
}

func (s *Server) handleSourcesCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	_, err := models.CreateSource(r.Context(), s.DB,
		strings.TrimSpace(r.FormValue("name")),
		strings.TrimSpace(r.FormValue("url")),
		strings.TrimSpace(r.FormValue("adapter")),
		enabled,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sources", http.StatusSeeOther)
}

func (s *Server) handleSourceEditGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	src, err := models.GetSource(r.Context(), s.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "source_edit", viewData{"Authed": true, "Source": src})
}

func (s *Server) handleSourceUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if err := models.UpdateSource(r.Context(), s.DB, id,
		strings.TrimSpace(r.FormValue("name")),
		strings.TrimSpace(r.FormValue("url")),
		strings.TrimSpace(r.FormValue("adapter")),
		enabled,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sources", http.StatusSeeOther)
}

func (s *Server) handleSourceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := models.DeleteSource(r.Context(), s.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sources", http.StatusSeeOther)
}

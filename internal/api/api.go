// Package api is the JSON HTTP API the CLI and UI talk to. It is a thin layer
// over the store plus the Executor for on-demand agent work.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/sarkar-ai-taken/hoofprint/internal/controlplane"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
)

// Server serves the HTTP API.
type Server struct {
	st      store.Store
	exec    controlplane.Executor
	log     *slog.Logger
	version string
	// WorkTimeout bounds synchronous agent work requested over HTTP.
	WorkTimeout time.Duration
}

func New(st store.Store, exec controlplane.Executor, log *slog.Logger, version string) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{st: st, exec: exec, log: log, version: version, WorkTimeout: 60 * time.Second}
}

// Handler returns the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/version", s.getVersion)
	mux.HandleFunc("GET /api/v1/agents", s.listAgents)
	mux.HandleFunc("GET /api/v1/agents/{id}", s.getAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/work", s.runWork)
	mux.HandleFunc("GET /api/v1/deploys", s.listDeploys)
	mux.HandleFunc("GET /api/v1/deploys/{id}", s.getDeploy)
	mux.HandleFunc("GET /api/v1/verdicts", s.listVerdicts)
	mux.HandleFunc("GET /api/v1/verdicts/{id}", s.getVerdict)
	return logRequests(s.log, mux)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) getVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.st.ListAgents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// WorkInput is the body of POST /api/v1/agents/{id}/work.
type WorkInput struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (s *Server) runWork(w http.ResponseWriter, r *http.Request) {
	var in WorkInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if in.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.WorkTimeout)
	defer cancel()
	res, err := s.exec.Execute(ctx, controlplane.WorkRequest{AgentID: r.PathValue("id"), Kind: in.Kind, Payload: in.Payload})
	if err != nil {
		switch {
		case errors.Is(err, controlplane.ErrNoAgent):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, controlplane.ErrAgentDisconnected):
			writeError(w, http.StatusBadGateway, err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "agent did not answer within "+s.WorkTimeout.String())
		default:
			s.fail(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listDeploys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	deploys, err := s.st.ListDeploys(r.Context(), model.DeployFilter{Cluster: q.Get("cluster"), Namespace: q.Get("namespace"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploys": deploys})
}

// DeployDetail is a deploy with its verdicts.
type DeployDetail struct {
	*model.Deploy
	Verdicts []*model.Verdict `json:"verdicts"`
}

func (s *Server) getDeploy(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDeploy(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	vs, err := s.st.ListVerdicts(r.Context(), d.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, DeployDetail{Deploy: d, Verdicts: vs})
}

func (s *Server) listVerdicts(w http.ResponseWriter, r *http.Request) {
	vs, err := s.st.ListVerdicts(r.Context(), r.URL.Query().Get("deploy_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"verdicts": vs})
}

// VerdictDetail is a verdict with its evidence bundle.
type VerdictDetail struct {
	*model.Verdict
	Evidence []*model.Evidence `json:"evidence"`
}

func (s *Server) getVerdict(w http.ResponseWriter, r *http.Request) {
	v, ev, err := s.st.GetVerdict(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, VerdictDetail{Verdict: v, Evidence: ev})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.log.Error("api error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		if r.URL.Path != "/healthz" {
			log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rw.code, "dur", time.Since(start))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

package web

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/patrickspencer/cronbat/internal/config"
	"github.com/patrickspencer/cronbat/internal/realtime"
	"github.com/patrickspencer/cronbat/internal/store"
	"github.com/patrickspencer/cronbat/internal/web/api"
	"github.com/patrickspencer/cronbat/internal/web/ui"
)

// Server is the HTTP server for the cronbat web interface and API.
type Server struct {
	httpServer *http.Server
}

// NewServer creates a new Server with the given dependencies.
func NewServer(
	addr string,
	s store.RunStore,
	events *realtime.Broker,
	getConfig func() *config.Config,
	jobs func() []*config.Job,
	jobState func(name string) string,
	createJob func(newJob config.Job) error,
	readRunLogs func(jobName string, runID string) (stdout string, stderr string, stdoutPath string, stderrPath string, err error),
	triggerFunc func(jobName string),
	nextRunTime func(name string) (time.Time, bool),
	enableJob func(name string) error,
	disableJob func(name string) error,
	startJob func(name string) error,
	stopJob func(name string) error,
	pauseJob func(name string) error,
	archiveJob func(name string) error,
	deleteJob func(name string) error,
	getJobYAML func(name string) (string, error),
	updateJobYAML func(name string, data string) (string, error),
	updateJobSettings func(name string, updated config.Job) error,
) *Server {
	mux := http.NewServeMux()

	a := &api.API{
		Store:             s,
		Events:            events,
		GetConfig:         getConfig,
		Jobs:              jobs,
		JobState:          jobState,
		CreateJob:         createJob,
		ReadRunLogs:       readRunLogs,
		TriggerRun:        triggerFunc,
		NextRunTime:       nextRunTime,
		EnableJob:         enableJob,
		DisableJob:        disableJob,
		StartJob:          startJob,
		StopJob:           stopJob,
		PauseJob:          pauseJob,
		ArchiveJob:        archiveJob,
		DeleteJob:         deleteJob,
		GetJobYAML:        getJobYAML,
		UpdateJobYAML:     updateJobYAML,
		UpdateJobSettings: updateJobSettings,
	}
	a.RegisterRoutes(mux)

	// Built-in minimal UI.
	mux.Handle("/ui/", http.StripPrefix("/ui/", ui.Handler()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	})

	return &Server{
		httpServer: &http.Server{
			Addr:    addr,
			Handler: corsMiddleware(mux),
		},
	}
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	log.Printf("http server listening on %s", ln.Addr().String())
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// corsMiddleware adds permissive CORS headers for development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

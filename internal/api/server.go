package api

import (
"context"
"io/fs"
"net/http"
"strings"
)

// RouteRegistrar is implemented by all API handler types.
type RouteRegistrar interface {
RegisterRoutes(mux *http.ServeMux)
}

// Server combines all OptiPilot REST API handlers and optionally serves the
// React dashboard SPA from an embedded filesystem.
//
// API routes are served at /api/v1/...
// All other GET requests fall through to the SPA (index.html) so that
// React Router client-side navigation works correctly.
type Server struct {
addr string
mux  *http.ServeMux
}

// NewServer creates a Server that listens on addr.
// handlers must be the RouteRegistrar objects for each sub-API.
// uiFS is the embedded ui/dashboard/dist directory; pass nil to skip SPA serving.
func NewServer(addr string, uiFS fs.FS, handlers ...RouteRegistrar) *Server {
mux := http.NewServeMux()

for _, h := range handlers {
h.RegisterRoutes(mux)
}

if uiFS != nil {
mux.Handle("/", spaHandler(uiFS))
}

return &Server{addr: addr, mux: mux}
}

// ServeHTTP implements http.Handler so the server can be used in tests directly.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
// Add CORS headers for local development usage of the dashboard.
w.Header().Set("Access-Control-Allow-Origin", "*")
w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
if r.Method == http.MethodOptions {
w.WriteHeader(http.StatusNoContent)
return
}
s.mux.ServeHTTP(w, r)
}

// Start begins listening and serving HTTP requests. It blocks until ctx is
// cancelled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
srv := &http.Server{
Addr:    s.addr,
Handler: s,
}
errCh := make(chan error, 1)
go func() {
if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
errCh <- err
}
}()
select {
case err := <-errCh:
return err
case <-ctx.Done():
return srv.Shutdown(context.Background())
}
}

// spaHandler returns an http.Handler that serves static files from uiFS.
// For paths that do not correspond to an existing file, it serves index.html
// so that React Router handles client-side routing.
func spaHandler(uiFS fs.FS) http.Handler {
fileServer := http.FileServer(http.FS(uiFS))
return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// API paths should not be handled by the SPA handler.
if strings.HasPrefix(r.URL.Path, "/api/") {
http.NotFound(w, r)
return
}
// Check if the requested file exists in the embedded FS.
path := strings.TrimPrefix(r.URL.Path, "/")
if path == "" {
path = "index.html"
}
if _, err := fs.Stat(uiFS, path); err != nil {
// File not found — serve index.html for SPA routing.
r2 := r.Clone(r.Context())
r2.URL.Path = "/"
http.ServeFileFS(w, r2, uiFS, "index.html")
return
}
fileServer.ServeHTTP(w, r)
})
}
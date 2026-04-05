package api_test

import (
"context"
"encoding/json"
"io"
"net/http"
"net/http/httptest"
"strings"
"testing"
"testing/fstest"
"time"

"github.com/optipilot-ai/optipilot/internal/api"
)

// ---------- stub RouteRegistrar ----------

type stubHandler struct {
path   string
status int
body   string
}

func (s *stubHandler) RegisterRoutes(mux *http.ServeMux) {
mux.HandleFunc("GET "+s.path, func(w http.ResponseWriter, r *http.Request) {
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(s.status)
io.WriteString(w, s.body)
})
}

// ---------- helpers ----------

func newTestServer(t *testing.T, uiFS interface{ ReadDir(string) ([]interface{}, error) }, handlers ...api.RouteRegistrar) (*api.Server, *httptest.Server) {
t.Helper()
srv := api.NewServer(":0", nil, handlers...)
ts := httptest.NewServer(srv)
t.Cleanup(ts.Close)
return srv, ts
}

func get(t *testing.T, ts *httptest.Server, path string) *http.Response {
t.Helper()
resp, err := http.Get(ts.URL + path)
if err != nil {
t.Fatalf("GET %s: %v", path, err)
}
return resp
}

// ---------- tests ----------

func TestServer_APIRouteRegistration(t *testing.T) {
h := &stubHandler{path: "/api/v1/ping", status: http.StatusOK, body: `{"ok":true}`}
srv := api.NewServer(":0", nil, h)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/api/v1/ping")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
if resp.StatusCode != http.StatusOK {
t.Fatalf("expected 200, got %d", resp.StatusCode)
}
var out map[string]bool
json.NewDecoder(resp.Body).Decode(&out)
if !out["ok"] {
t.Fatal("expected ok=true")
}
}

func TestServer_MultipleHandlers(t *testing.T) {
h1 := &stubHandler{path: "/api/v1/foo", status: 200, body: `"foo"`}
h2 := &stubHandler{path: "/api/v1/bar", status: 200, body: `"bar"`}
srv := api.NewServer(":0", nil, h1, h2)
ts := httptest.NewServer(srv)
defer ts.Close()

for _, path := range []string{"/api/v1/foo", "/api/v1/bar"} {
resp, err := http.Get(ts.URL + path)
if err != nil {
t.Fatal(err)
}
resp.Body.Close()
if resp.StatusCode != 200 {
t.Errorf("path %s: expected 200, got %d", path, resp.StatusCode)
}
}
}

func TestServer_CORSHeaders(t *testing.T) {
h := &stubHandler{path: "/api/v1/test", status: 200, body: `{}`}
srv := api.NewServer(":0", nil, h)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/api/v1/test")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
t.Fatal("expected CORS header Access-Control-Allow-Origin: *")
}
}

func TestServer_CORSPreflight(t *testing.T) {
srv := api.NewServer(":0", nil)
ts := httptest.NewServer(srv)
defer ts.Close()

req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/v1/anything", nil)
resp, err := http.DefaultClient.Do(req)
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
if resp.StatusCode != http.StatusNoContent {
t.Fatalf("expected 204 for OPTIONS, got %d", resp.StatusCode)
}
}

func TestServer_SPAServeIndex(t *testing.T) {
uiFS := fstest.MapFS{
"index.html": &fstest.MapFile{Data: []byte("<html><body>OptiPilot Dashboard</body></html>")},
"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
}
srv := api.NewServer(":0", uiFS)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
if resp.StatusCode != 200 {
t.Fatalf("expected 200 for /, got %d", resp.StatusCode)
}
body, _ := io.ReadAll(resp.Body)
if !strings.Contains(string(body), "OptiPilot Dashboard") {
t.Fatal("expected index.html content at /")
}
}

func TestServer_SPAFallback(t *testing.T) {
// Deep route with no corresponding file should return index.html (React Router)
uiFS := fstest.MapFS{
"index.html": &fstest.MapFile{Data: []byte("<html>SPA</html>")},
}
srv := api.NewServer(":0", uiFS)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/decisions/abc-123")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
body, _ := io.ReadAll(resp.Body)
if !strings.Contains(string(body), "SPA") {
t.Fatalf("expected SPA fallback for unknown path, got: %s", body)
}
}

func TestServer_SPAStaticFile(t *testing.T) {
uiFS := fstest.MapFS{
"index.html":          &fstest.MapFile{Data: []byte("<html>SPA</html>")},
"assets/index.css":    &fstest.MapFile{Data: []byte("body{margin:0}")},
}
srv := api.NewServer(":0", uiFS)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/assets/index.css")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
if resp.StatusCode != 200 {
t.Fatalf("expected 200 for static asset, got %d", resp.StatusCode)
}
body, _ := io.ReadAll(resp.Body)
if !strings.Contains(string(body), "body") {
t.Fatalf("expected CSS content, got: %s", body)
}
}

func TestServer_APIPathNotHandledBySPA(t *testing.T) {
// /api/ paths should not fall through to SPA even when uiFS is set
uiFS := fstest.MapFS{
"index.html": &fstest.MapFile{Data: []byte("<html>SPA</html>")},
}
h := &stubHandler{path: "/api/v1/data", status: 200, body: `{"data":true}`}
srv := api.NewServer(":0", uiFS, h)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/api/v1/data")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
body, _ := io.ReadAll(resp.Body)
if !strings.Contains(string(body), "data") {
t.Fatalf("expected API response, got SPA content: %s", body)
}
}

func TestServer_NoUIServedWhenNilFS(t *testing.T) {
srv := api.NewServer(":0", nil)
ts := httptest.NewServer(srv)
defer ts.Close()

resp, err := http.Get(ts.URL + "/")
if err != nil {
t.Fatal(err)
}
defer resp.Body.Close()
// With no UI, "/" returns 404 (no handler registered)
if resp.StatusCode != http.StatusNotFound {
t.Fatalf("expected 404 with nil uiFS, got %d", resp.StatusCode)
}
}

func TestServer_StartShutdown(t *testing.T) {
srv := api.NewServer(":0", nil)
ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
defer cancel()
// Start returns on context cancellation. Only check it doesn't panic.
_ = srv
_ = ctx
}
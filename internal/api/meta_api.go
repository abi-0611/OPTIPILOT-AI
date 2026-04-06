package api

import (
	"net/http"
)

// MetaHandler exposes process/cluster metadata for the dashboard.
type MetaHandler struct {
	ClusterName string
}

// NewMetaHandler returns a handler. clusterName may be empty (UI shows "unknown").
func NewMetaHandler(clusterName string) *MetaHandler {
	return &MetaHandler{ClusterName: clusterName}
}

// RegisterRoutes registers GET /api/v1/meta.
func (h *MetaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/meta", h.handleMeta)
}

type metaResponse struct {
	ClusterName string `json:"cluster_name"`
}

func (h *MetaHandler) handleMeta(w http.ResponseWriter, _ *http.Request) {
	name := h.ClusterName
	if name == "" {
		name = "local"
	}
	writeJSON(w, metaResponse{ClusterName: name})
}

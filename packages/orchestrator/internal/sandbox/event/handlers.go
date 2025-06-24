package event

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"go.uber.org/zap"
)

type EventHandler interface {
	Path() string
	HandlerFunc(w http.ResponseWriter, r *http.Request)
}

type MetricsHandler struct{}

func (h *MetricsHandler) Path() string {
	return "/metrics"
}

func (h *MetricsHandler) HandlerFunc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, err := w.Write([]byte(`{"event_ack":true,"path":"/metrics"}`))
	if err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		return
	}
}

// This handler is used to store event data for all paths that are not registered in the event server.
// This is used to track ad-hoc events that are not handled by the event server.
type DefaultHandler struct {
	store SandboxEventStore
}

func (h *DefaultHandler) Path() string {
	return "/"
}

func (h *DefaultHandler) HandlerFunc(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.Header.Get("E2B_SANDBOX_ID")
	if r.Method == http.MethodGet {
		event, err := h.store.GetEvent(sandboxID)
		if err != nil {
			zap.L().Error("Failed to get event data for sandbox "+sandboxID, zap.Error(err))
			http.Error(w, "Failed to get event data for sandbox "+sandboxID, http.StatusInternalServerError)
			return
		}

		eventJSON, err := json.Marshal(event)
		if err != nil {
			zap.L().Error("Failed to marshal event data", zap.Error(err))
			http.Error(w, "Failed to marshal event data", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(eventJSON)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Create event data with path and body
	eventData := SandboxEvent{
		Path: r.URL.Path,
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	eventData.Body = make(map[string]any)
	err = json.Unmarshal(body, &eventData.Body)
	if err != nil {
		http.Error(w, "Failed to unmarshal request body", http.StatusInternalServerError)
		return
	}

	// Store in Redis with sandboxID as key
	err = h.store.SetEvent(sandboxID, &eventData, 0)
	if err != nil {
		zap.L().Error("Failed to store event data", zap.Error(err))
		http.Error(w, "Failed to store event data", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"event_ack":true}`))
}

func NewEventHandlers(ctx context.Context, store SandboxEventStore) []EventHandler {
	return []EventHandler{
		&MetricsHandler{},
		&DefaultHandler{store},
	}
}

package events

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

type SandboxEventHandler interface {
	Path() string
	HandlerFunc(w http.ResponseWriter, r *http.Request)
}

// This is used to track ad-hoc events that are not handled by the event server.
type DefaultSandboxEventHandler struct {
	store SandboxEventStore
}

func NewDefaultSandboxEventHandler(store SandboxEventStore) *DefaultSandboxEventHandler {
	return &DefaultSandboxEventHandler{store}
}

func (h *DefaultSandboxEventHandler) Path() string {
	return "/"
}

func (h *DefaultSandboxEventHandler) HandlerFunc(w http.ResponseWriter, r *http.Request) {
	addr := r.RemoteAddr
	ip := strings.Split(addr, ":")[0]
	sandboxID, err := h.store.GetSandboxIP(r.Context(), ip)
	if err != nil {
		zap.L().Error("Failed to get sandbox ID from IP", zap.Error(err))
		http.Error(w, "Error handling event", http.StatusInternalServerError)
		return
	}

	zap.L().Debug("Received request from sandbox", zap.String("sandbox_id", sandboxID), zap.String("ip", ip))

	if r.Method == http.MethodGet {
		events, err := h.store.GetLastNEvents(r.Context(), sandboxID, 10)
		if err != nil {
			zap.L().Error("Failed to get event data for sandbox "+sandboxID, zap.Error(err))
			http.Error(w, "Failed to get event data for sandbox "+sandboxID, http.StatusInternalServerError)
			return
		}

		eventJSON, err := json.Marshal(events)
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

	zap.L().Info("Received event", zap.String("body", string(body)))

	eventData.Body = make(map[string]any)
	err = json.Unmarshal(body, &eventData.Body)
	if err != nil {
		zap.L().Error("Failed to unmarshal request body", zap.Error(err))
		http.Error(w, "Failed to unmarshal request body", http.StatusInternalServerError)
		return
	}

	// Store in Redis with sandboxID as key
	err = h.store.AddEvent(r.Context(), sandboxID, &eventData, 0)
	if err != nil {
		zap.L().Error("Failed to store event data", zap.Error(err))
		http.Error(w, "Failed to store event data", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"event_ack":true}`))
}

package event

import "net/http"

type EventHandler struct {
	Path        string
	HandlerFunc func(w http.ResponseWriter, r *http.Request)
}

var MetricsHandler = EventHandler{
	Path: "/metrics",
	HandlerFunc: func(w http.ResponseWriter, r *http.Request) {
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
	},
}

var DefaultHandler = EventHandler{
	Path: "/",
	HandlerFunc: func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found", http.StatusNotFound)
	},
}

var EventHandlers = []EventHandler{
	MetricsHandler,
	DefaultHandler,
}

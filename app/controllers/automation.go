package controllers

import (
	"encoding/json"
	"net/http"
)

func apiAutomationHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/automation/" {
		APIError(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		APIError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ai := activeAI.Load()
	if ai == nil || ai.snapshot.Load() == nil {
		APIError(w, "automation has not started", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ai.snapshot.Load().status)
}

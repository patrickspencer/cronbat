package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/patrickspencer/cronbat/internal/realtime"
)

func (a *API) emitEvent(evt realtime.Event) {
	if a.Events == nil {
		return
	}
	a.Events.Publish(evt)
}

func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.Events == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "realtime stream unavailable"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Initial comment opens the stream cleanly in browsers/proxies.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	events, cancel := a.Events.Subscribe()
	defer cancel()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}

			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", evt.ID, evt.Type, payload); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

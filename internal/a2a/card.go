package a2a

import (
	"encoding/json"
	"net/http"
)

// ServeAgentCard returns an http.HandlerFunc that serves the agent card JSON
// at /.well-known/agent.json. The card is marshalled once at startup.
func ServeAgentCard(card AgentCard) http.HandlerFunc {
	data, _ := json.Marshal(card)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

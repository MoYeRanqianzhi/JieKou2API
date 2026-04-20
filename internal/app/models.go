package app

import (
	"encoding/json"
	"net/http"
)

type ModelInfo struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

type ModelsResponse struct {
	Data    []ModelInfo `json:"data"`
	HasMore bool        `json:"has_more"`
	FirstID string      `json:"first_id"`
	LastID  string      `json:"last_id"`
}

var supportedModels = []ModelInfo{
	{
		ID:          "claude-opus-4-7",
		Object:      "model",
		Created:     1700000000,
		OwnedBy:    "anthropic",
		DisplayName: "Claude Opus 4.7",
	},
}

func ModelsHandler(w http.ResponseWriter, r *http.Request) {
	firstID, lastID := "", ""
	if len(supportedModels) > 0 {
		firstID = supportedModels[0].ID
		lastID = supportedModels[len(supportedModels)-1].ID
	}

	resp := ModelsResponse{
		Data:    supportedModels,
		HasMore: false,
		FirstID: firstID,
		LastID:  lastID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

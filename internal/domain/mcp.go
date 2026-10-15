package domain

import "time"

// MCPCall is one append-only audit record of an MCP tool call (SEC-MCP-08).
type MCPCall struct {
	ID          int64     `json:"id"`
	At          time.Time `json:"at"`
	Tool        string    `json:"tool"`
	ArgsSummary string    `json:"args_summary,omitempty"`
	Status      string    `json:"status"`
	DurationMS  int64     `json:"duration_ms"`
	ItemCount   int64     `json:"item_count"`
}

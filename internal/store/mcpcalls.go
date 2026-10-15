package store

import (
	"context"
	"fmt"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// AppendMCPCall records an MCP tool call (SEC-MCP-08).
func (s *Store) AppendMCPCall(ctx context.Context, c domain.MCPCall) error {
	const q = `INSERT INTO mcp_calls (at, tool, args_summary, status, duration_ms, item_count)
	           VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q, formatTime(c.At), c.Tool, c.ArgsSummary, c.Status, c.DurationMS, c.ItemCount)
	if err != nil {
		return fmt.Errorf("store: append mcp call: %w", err)
	}
	return nil
}

// MCPCalls returns the most recent MCP call records (newest first). A limit
// <= 0 returns every record.
func (s *Store) MCPCalls(ctx context.Context, limit int) ([]domain.MCPCall, error) {
	q := `SELECT id, at, tool, args_summary, status, duration_ms, item_count
	      FROM mcp_calls ORDER BY id DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query mcp calls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.MCPCall
	for rows.Next() {
		var (
			c  domain.MCPCall
			at string
		)
		if err := rows.Scan(&c.ID, &at, &c.Tool, &c.ArgsSummary, &c.Status,
			&c.DurationMS, &c.ItemCount); err != nil {
			return nil, fmt.Errorf("store: scan mcp call row: %w", err)
		}
		c.At = parseTime(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

package domain

import "time"

// PlanStatus is the lifecycle state of a cleanup plan.
type PlanStatus string

const (
	// PlanPending is an unexpired plan that has not been applied.
	PlanPending PlanStatus = "pending"
	// PlanApplied means the plan was applied (possibly partially).
	PlanApplied PlanStatus = "applied"
	// PlanExpired means the plan is past its expiry.
	PlanExpired PlanStatus = "expired"
	// PlanFailed means apply was attempted and aborted.
	PlanFailed PlanStatus = "failed"
)

// Plan is an immutable cleanup plan (SEC-DEL-01).
type Plan struct {
	PlanID    string     `json:"plan_id"`
	Hash      string     `json:"hash"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	Status    PlanStatus `json:"status"`
	Items     []PlanItem `json:"items"`
	TotalSize int64      `json:"total_size"`
}

// Expired reports whether the plan is past its expiry at now.
func (p Plan) Expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

// PlanItem is one item captured in a plan. It records the identity needed for
// apply-time revalidation (SEC-DEL-06).
type PlanItem struct {
	PlanID    string    `json:"plan_id"`
	EntryID   string    `json:"entry_id"`
	Path      string    `json:"path"`
	Dev       uint64    `json:"dev"`
	Inode     uint64    `json:"inode"`
	Size      int64     `json:"size"`
	MTime     time.Time `json:"mtime"`
	DataClass DataClass `json:"data_class"`
}

// Consent is a recorded, per-root grant (SEC-ACC-07).
type Consent struct {
	Root      string    `json:"root"`
	Tier      Tier      `json:"tier"`
	GrantedAt time.Time `json:"granted_at"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

// Active reports whether the consent is currently in force at now.
func (c Consent) Active(now time.Time) bool {
	return c.RevokedAt.IsZero() || c.RevokedAt.After(now)
}

// AuditEntry is one append-only record of an applied action (SEC-TRN-07).
type AuditEntry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	Path   string    `json:"path"`
	Size   int64     `json:"size"`
	PlanID string    `json:"plan_id,omitempty"`
	Result string    `json:"result"`
	Detail string    `json:"detail,omitempty"`
}

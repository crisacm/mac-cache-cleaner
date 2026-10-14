package cleaner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/platform"
)

// ApplyOptions configures applying a plan.
type ApplyOptions struct {
	PlanID string
	// Confirm is called before any destructive action and must return true.
	// It receives the item count and total size for the prompt (SEC-DEL-02).
	Confirm func(count int, totalBytes int64) (bool, error)
	// Permanent enables permanent deletion; it requires ConfirmPermanent to
	// return true as well (SEC-DEL-04).
	Permanent bool
	// ConfirmPermanent is the extra confirmation for permanent deletion.
	ConfirmPermanent func(count int, totalBytes int64) (bool, error)
	// MaxItems / MaxBytes are extra apply-time limits (SEC-DEL-11).
	MaxItems int
	MaxBytes int64
}

// ApplyResult summarizes an apply run.
type ApplyResult struct {
	PlanID  string    `json:"plan_id"`
	Applied []Outcome `json:"applied"`
	Skipped []Outcome `json:"skipped"`
	Failed  []Outcome `json:"failed"`
}

// Apply executes a valid, unexpired plan, revalidating every item
// (SEC-DEL-01, SEC-DEL-06).
func (s *Service) Apply(ctx context.Context, opts ApplyOptions) (ApplyResult, error) {
	if opts.PlanID == "" {
		return ApplyResult{}, fmt.Errorf("%w: plan id is required", domain.ErrUsage)
	}
	if opts.Confirm == nil {
		return ApplyResult{}, fmt.Errorf("%w: interactive confirmation is required (SEC-DEL-02)", domain.ErrUsage)
	}

	plan, err := s.store.GetPlan(ctx, opts.PlanID)
	if err != nil {
		return ApplyResult{}, err
	}
	items, err := s.store.PlanItems(ctx, opts.PlanID)
	if err != nil {
		return ApplyResult{}, err
	}
	plan.Items = items

	now := s.now()
	if plan.Status == domain.PlanApplied {
		return ApplyResult{}, fmt.Errorf("%w: plan already applied", domain.ErrBlocked)
	}
	if plan.Expired(now) {
		_ = s.store.UpdatePlanStatus(ctx, plan.PlanID, domain.PlanExpired)
		return ApplyResult{}, fmt.Errorf("%w: plan expired at %s", domain.ErrBlocked, plan.ExpiresAt.Format(time.RFC3339))
	}
	if got := hashPlanItems(items); got != plan.Hash {
		_ = s.store.UpdatePlanStatus(ctx, plan.PlanID, domain.PlanFailed)
		return ApplyResult{}, fmt.Errorf("%w: plan hash mismatch", domain.ErrBlocked)
	}

	if opts.MaxItems > 0 && len(items) > opts.MaxItems {
		return ApplyResult{}, fmt.Errorf("%w: plan has %d items, over the apply limit of %d", domain.ErrBlocked, len(items), opts.MaxItems)
	}
	total := plan.TotalSize
	if opts.MaxBytes > 0 && total > opts.MaxBytes {
		return ApplyResult{}, fmt.Errorf("%w: plan is %d bytes, over the apply limit of %d", domain.ErrBlocked, total, opts.MaxBytes)
	}

	ok, err := opts.Confirm(len(items), total)
	if err != nil {
		return ApplyResult{}, err
	}
	if !ok {
		return ApplyResult{}, fmt.Errorf("%w: cancelled by user", domain.ErrBlocked)
	}

	if opts.Permanent {
		if opts.ConfirmPermanent == nil {
			return ApplyResult{}, fmt.Errorf("%w: permanent deletion needs an extra confirmation", domain.ErrBlocked)
		}
		ok, err := opts.ConfirmPermanent(len(items), total)
		if err != nil {
			return ApplyResult{}, err
		}
		if !ok {
			return ApplyResult{}, fmt.Errorf("%w: permanent deletion cancelled", domain.ErrBlocked)
		}
	}

	running := map[string]struct{}{}
	if s.running != nil {
		if ids, err := s.running.BundleIDs(ctx); err == nil {
			running = ids
		}
	}

	result := ApplyResult{PlanID: plan.PlanID}
	for _, item := range items {
		outcome := Outcome{EntryID: item.EntryID, Path: item.Path}

		if err := s.revalidate(item); err != nil {
			outcome.Reason = err.Error()
			result.Skipped = append(result.Skipped, outcome)
			s.audit(ctx, plan.PlanID, "skip", item, outcome.Reason)
			continue
		}
		if id := bundleIDFromPath(item.Path); id != "" {
			if _, busy := running[id]; busy {
				outcome.Reason = "application is running"
				result.Skipped = append(result.Skipped, outcome)
				s.audit(ctx, plan.PlanID, "skip", item, outcome.Reason)
				continue
			}
		}

		action := "trash"
		if opts.Permanent {
			action = "delete"
			err = os.RemoveAll(item.Path) // #nosec G304 -- item.Path is a validated plan path
		} else {
			err = s.trash.Trash(ctx, item.Path)
		}
		if err != nil {
			outcome.Reason = err.Error()
			result.Failed = append(result.Failed, outcome)
			s.audit(ctx, plan.PlanID, action+":failed", item, outcome.Reason)
			continue
		}
		result.Applied = append(result.Applied, outcome)
		s.audit(ctx, plan.PlanID, action, item, "")
	}

	status := domain.PlanApplied
	if len(result.Failed) > 0 && len(result.Applied) == 0 {
		status = domain.PlanFailed
	}
	if err := s.store.UpdatePlanStatus(ctx, plan.PlanID, status); err != nil {
		return result, err
	}
	return result, nil
}

// revalidate re-checks a plan item immediately before acting (SEC-DEL-06).
func (s *Service) revalidate(item domain.PlanItem) error {
	v := s.policy.DeleteVerdict(item.Path, "")
	if !v.Allowed() {
		return fmt.Errorf("no longer allowed: %s", v.Reason)
	}
	fi, err := os.Lstat(item.Path)
	if err != nil {
		return fmt.Errorf("no longer present")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("is now a symlink")
	}
	if si, ok := platform.Stat(fi); ok {
		if si.Dev != item.Dev || si.Inode != item.Inode {
			return errors.New("identity changed (dev/inode)")
		}
		// Size is compared for files only: a directory entry carries its
		// subtree size, which lstat cannot reproduce. Directory content
		// changes are caught by the mtime check below.
		if !fi.IsDir() && si.AllocatedBytes != item.Size {
			return errors.New("size changed")
		}
	}
	if !fi.ModTime().Equal(item.MTime) {
		return errors.New("mtime changed")
	}
	return nil
}

func (s *Service) audit(ctx context.Context, planID string, action string, item domain.PlanItem, detail string) {
	_ = s.store.AppendAudit(ctx, domain.AuditEntry{
		At:     s.now(),
		Action: action,
		Path:   item.Path,
		Size:   item.Size,
		PlanID: planID,
		Result: action,
		Detail: detail,
	})
}

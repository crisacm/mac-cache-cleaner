package domain

import "fmt"

// DataClass labels what kind of data an entry holds. It travels with every
// result (SEC-ACC-08) and determines cleanup eligibility (SEC-DEL-03).
type DataClass string

const (
	// ClassRegenerableCache is data an application can regenerate. It may be
	// included in an automated plan.
	ClassRegenerableCache DataClass = "regenerable_cache"
	// ClassAppData is application data that may contain user data. It is only
	// planned after explicit per-item human selection.
	ClassAppData DataClass = "app_data"
	// ClassUserFile is user data (Tier 1). Report-only: never automated.
	ClassUserFile DataClass = "user_file"
	// ClassApplication is an installed application. Report-only: MCC never
	// uninstalls applications.
	ClassApplication DataClass = "application"
)

// DataClasses lists every valid data class.
var DataClasses = []DataClass{
	ClassRegenerableCache,
	ClassAppData,
	ClassUserFile,
	ClassApplication,
}

// Valid reports whether c is a known data class.
func (c DataClass) Valid() bool {
	switch c {
	case ClassRegenerableCache, ClassAppData, ClassUserFile, ClassApplication:
		return true
	default:
		return false
	}
}

// String returns the wire representation of the data class.
func (c DataClass) String() string { return string(c) }

// PlanEligible reports whether the class may be included in an automated plan
// without the user selecting the item explicitly (SEC-DEL-03).
func (c DataClass) PlanEligible() bool { return c == ClassRegenerableCache }

// ReportOnly reports whether the class must never appear in any plan.
func (c DataClass) ReportOnly() bool {
	return c == ClassUserFile || c == ClassApplication
}

// CleanupEligibility tells a caller (agent, UI) whether an item may be planned.
type CleanupEligibility string

const (
	// EligibilityAuto may be planned without per-item selection.
	EligibilityAuto CleanupEligibility = "auto_plan"
	// EligibilityExplicitSelection requires an explicit per-item human choice.
	EligibilityExplicitSelection CleanupEligibility = "explicit_selection_required"
	// EligibilityReportOnly must never be planned.
	EligibilityReportOnly CleanupEligibility = "report_only"
)

// Eligibility maps a data class to its cleanup eligibility (SEC-DEL-03).
func (c DataClass) Eligibility() CleanupEligibility {
	switch c {
	case ClassRegenerableCache:
		return EligibilityAuto
	case ClassAppData:
		return EligibilityExplicitSelection
	default:
		return EligibilityReportOnly
	}
}

// MayContainUserData reports whether planning the class requires the
// "may contain user data" warning.
func (c DataClass) MayContainUserData() bool { return c == ClassAppData }

// ParseDataClass parses the wire representation of a data class.
func ParseDataClass(s string) (DataClass, error) {
	c := DataClass(s)
	if !c.Valid() {
		return "", fmt.Errorf("domain: unknown data class %q", s)
	}
	return c, nil
}

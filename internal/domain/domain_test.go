package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDataClassProperties(t *testing.T) {
	tests := []struct {
		class        DataClass
		planEligible bool
		reportOnly   bool
	}{
		{ClassRegenerableCache, true, false},
		{ClassAppData, false, false},
		{ClassUserFile, false, true},
		{ClassApplication, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.class.String(), func(t *testing.T) {
			require.True(t, tt.class.Valid())
			require.Equal(t, tt.planEligible, tt.class.PlanEligible())
			require.Equal(t, tt.reportOnly, tt.class.ReportOnly())
		})
	}
	require.False(t, DataClass("bogus").Valid())
	_, err := ParseDataClass("bogus")
	require.Error(t, err)
	c, err := ParseDataClass("app_data")
	require.NoError(t, err)
	require.Equal(t, ClassAppData, c)
}

func TestTierConsent(t *testing.T) {
	require.False(t, TierDefault.RequiresConsent())
	require.True(t, TierUserData.RequiresConsent())
	require.False(t, TierSensitive.RequiresConsent())
	require.True(t, TierVolume.RequiresConsent())
	require.False(t, Tier(9).Valid())
}

func TestConfidenceRank(t *testing.T) {
	require.Greater(t, ConfidenceHigh.Rank(), ConfidenceMedium.Rank())
	require.Greater(t, ConfidenceMedium.Rank(), ConfidenceLow.Rank())
}

func TestComputeEntryIDStable(t *testing.T) {
	a := ComputeEntryID("scan-1", "/Users/x/Library/Caches")
	b := ComputeEntryID("scan-1", "/Users/x/Library/Caches")
	require.Equal(t, a, b, "entry ID must be deterministic")
	require.NotEqual(t, a, ComputeEntryID("scan-2", "/Users/x/Library/Caches"))
	require.Len(t, a, 32)
}

func TestNewIDUnique(t *testing.T) {
	a, err := NewID()
	require.NoError(t, err)
	b, err := NewID()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.Len(t, a, 32)
}

func TestPlanExpiry(t *testing.T) {
	base := time.Now()
	p := Plan{ExpiresAt: base.Add(time.Minute)}
	require.False(t, p.Expired(base.Add(30*time.Second)))
	require.True(t, p.Expired(base.Add(2*time.Minute)))
}

func TestConsentActive(t *testing.T) {
	base := time.Now()
	require.True(t, Consent{GrantedAt: base}.Active(base))
	require.False(t, Consent{GrantedAt: base, RevokedAt: base}.Active(base))
}

func TestEligibility(t *testing.T) {
	require.Equal(t, EligibilityAuto, ClassRegenerableCache.Eligibility())
	require.Equal(t, EligibilityExplicitSelection, ClassAppData.Eligibility())
	require.Equal(t, EligibilityReportOnly, ClassUserFile.Eligibility())
	require.Equal(t, EligibilityReportOnly, ClassApplication.Eligibility())
}

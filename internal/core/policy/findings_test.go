package policy_test

import (
	"errors"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

func finding(sev model.Severity, cat model.FindingCategory, lvl model.SecurityLevel) model.Finding {
	return model.Finding{ID: "f", Severity: sev, Category: cat, SecurityLevel: lvl}
}

func TestIsBlocking(t *testing.T) {
	tests := []struct {
		name    string
		f       model.Finding
		want    bool
		wantErr bool
	}{
		{"§6.3 P0 blocks", finding(model.SeverityP0, model.FindingCategoryCorrectness, ""), true, false},
		{"§6.3 P1 blocks", finding(model.SeverityP1, model.FindingCategoryDocs, ""), true, false},
		{"§6.3 P2 correctness does not block", finding(model.SeverityP2, model.FindingCategoryCorrectness, ""), false, false},
		{"§6.3 P3 correctness does not block", finding(model.SeverityP3, model.FindingCategoryCorrectness, ""), false, false},
		{"§6.3 security High blocks", finding(model.SeverityP3, model.FindingCategorySecurity, model.SecurityLevelHigh), true, false},
		{"§6.3 security Medium blocks", finding(model.SeverityP2, model.FindingCategorySecurity, model.SecurityLevelMedium), true, false},
		{"§6.3 security Low does not block", finding(model.SeverityP2, model.FindingCategorySecurity, model.SecurityLevelLow), false, false},
		{"§6.3 Medium blocks in any category", finding(model.SeverityP3, model.FindingCategoryData, model.SecurityLevelMedium), true, false},
		{"§6.3 High blocks in any category", finding(model.SeverityP3, model.FindingCategoryCorrectness, model.SecurityLevelHigh), true, false},
		{"§6.3 hard-stop P3 blocks", finding(model.SeverityP3, model.FindingCategoryHardStop, ""), true, false},
		{"security without level", finding(model.SeverityP3, model.FindingCategorySecurity, ""), false, true},
		{"empty severity", finding("", model.FindingCategoryCorrectness, ""), false, true},
		{"unknown severity", finding("P4", model.FindingCategoryCorrectness, ""), false, true},
		{"unknown category", finding(model.SeverityP3, "style", ""), false, true},
		{"unknown security level", finding(model.SeverityP3, model.FindingCategorySecurity, "Critical"), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.IsBlocking(tt.f)
			if tt.wantErr != errors.Is(err, policy.ErrInvalidFinding) || (!tt.wantErr && err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBlockingCount(t *testing.T) {
	ok := []model.Finding{
		finding(model.SeverityP0, model.FindingCategoryCorrectness, ""),
		finding(model.SeverityP2, model.FindingCategoryDocs, ""),
		finding(model.SeverityP3, model.FindingCategoryHardStop, ""),
	}
	if n, err := policy.BlockingCount(ok); err != nil || n != 2 {
		t.Fatalf("BlockingCount = %d, %v; want 2", n, err)
	}
	if n, err := policy.BlockingCount(nil); err != nil || n != 0 {
		t.Fatalf("BlockingCount(nil) = %d, %v", n, err)
	}
	bad := append(ok, finding(model.SeverityP3, model.FindingCategorySecurity, ""))
	if n, err := policy.BlockingCount(bad); !errors.Is(err, policy.ErrInvalidFinding) || n != 0 {
		t.Fatalf("BlockingCount(invalid) = %d, %v; want 0 and ErrInvalidFinding", n, err)
	}
}

package static

import "testing"

func TestRejectsHistoricalTagDeletion(t *testing.T) {
	if err := CheckText("workflow.yml", "gh release delete v1.2.3 --cleanup-tag"); err == nil {
		t.Fatal("expected invariant violation")
	}
}

func TestAllowsStableChannelTagPromotion(t *testing.T) {
	if err := CheckText("infra-release.yml", "git push origin refs/tags/v1 --force"); err != nil {
		t.Fatalf("stable channel promotion was rejected: %v", err)
	}
}

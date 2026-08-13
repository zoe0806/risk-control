package tools

import "testing"

func TestStripCompanySuffix(t *testing.T) {
	got := StripCompanySuffix("EVIL_ENTITY_LTD")
	if got != "EVIL_ENTITY" {
		t.Fatalf("got %q", got)
	}
	got = StripCompanySuffix("ACME_CORPORATION")
	if got != "ACME" {
		t.Fatalf("got %q", got)
	}
}

func TestSimilarityRatio(t *testing.T) {
	if SimilarityRatio("ROSNEFT_OIL", "ROSNEFT_OIL") != 1 {
		t.Fatal("exact")
	}
	s := SimilarityRatio("ROSNEFT", "ROSNEFT_OIL")
	if s < 0.5 {
		t.Fatalf("expected decent similarity, got %f", s)
	}
}

func TestRankCandidates(t *testing.T) {
	party := NormalizePartyName("Evil Entity Ltd", "US")
	cands := []SanctionCandidate{
		{NameOriginal: "Other", NameNormalized: "OTHER_CO"},
		{NameOriginal: "Evil Entity Limited", NameNormalized: "EVIL_ENTITY_LTD", Aliases: []string{"Evil Entity"}},
	}
	out := RankCandidates(party, cands, 0.5, 8)
	if len(out) == 0 {
		t.Fatal("expected hits")
	}
	if out[0].NameNormalized != "EVIL_ENTITY_LTD" {
		t.Fatalf("top=%+v", out[0])
	}
	if out[0].MatchScore < 0.8 {
		t.Fatalf("score too low: %f", out[0].MatchScore)
	}
}

func TestDecisionFromScore(t *testing.T) {
	if DecisionFromScore(0.1) != DecisionApprove {
		t.Fatal()
	}
	if DecisionFromScore(0.5) != DecisionReview {
		t.Fatal()
	}
	if DecisionFromScore(0.8) != DecisionReject {
		t.Fatal()
	}
}

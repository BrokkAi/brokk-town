package simplifierbot

import (
	"strings"
	"testing"
)

func TestParseAssessment(t *testing.T) {
	got, err := parseAssessment("discussion\nSIMPLIFY_RESULT {\"decision\":\"decline\",\"summary\":\"Low value\",\"detail\":\"The subsystem has one caller and no acceptance test.\"}")
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != "decline" || got.Summary != "Low value" || !strings.Contains(got.Detail, "one caller") {
		t.Fatalf("unexpected assessment: %+v", got)
	}
}

func TestParseAssessmentRejectsUnknownDecision(t *testing.T) {
	if _, err := parseAssessment(`SIMPLIFY_RESULT {"decision":"delete","summary":"x","detail":"y"}`); err == nil {
		t.Fatal("accepted an unknown decision")
	}
}

func TestParseScanBoundedProposals(t *testing.T) {
	text := `SIMPLIFY_SCAN {"summary":"Two candidates","proposals":[{"title":"Remove plugin registry","concern":"unused","alternative":"direct construction","subsystems":["internal/registry"],"evidence":["no callers"]},{"title":"Remove format matrix","concern":"untested","alternative":"JSON only","subsystems":["internal/format"],"evidence":["no tests"]}]}`
	proposals, summary, err := parseScan(text, 1)
	if err == nil {
		t.Fatal("accepted more proposals than max_proposals")
	}
	if summary != "" || proposals != nil {
		t.Fatal("failed parse returned data")
	}
}

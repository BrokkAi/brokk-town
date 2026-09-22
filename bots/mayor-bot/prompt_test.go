package mayorbot

import (
	"strings"
	"testing"
)

func TestParseJudgment(t *testing.T) {
	got, err := parseJudgment("weighed it\nMAYOR_DECISION {\"decision\":\"admit\",\"reason\":\"A real defect with a bounded fix.\"}", "issue")
	if err != nil || got.Decision != "admit" || !strings.Contains(got.Reason, "bounded fix") {
		t.Fatalf("unexpected judgment %+v (%v)", got, err)
	}
	for name, text := range map[string]string{
		"unknown decision":  `MAYOR_DECISION {"decision":"merge","reason":"ship it"}`,
		"missing reason":    `MAYOR_DECISION {"decision":"admit","reason":"  "}`,
		"unknown field":     `MAYOR_DECISION {"decision":"admit","reason":"ok","score":1}`,
		"no receipt at all": `I admit it.`,
		"receipt not last":  "MAYOR_DECISION {\"decision\":\"admit\",\"reason\":\"ok\"}\nmore text",
		"oversized reason":  `MAYOR_DECISION {"decision":"admit","reason":"` + strings.Repeat("x", 2001) + `"}`,
	} {
		if _, err := parseJudgment(text, "issue"); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestParseBulletinBoundsItemsToTheWindow(t *testing.T) {
	text := `MAYOR_BULLETIN {"title":"Faster exports","summary":"Exports finish sooner and no longer lose filters.","items":[{"kind":"fix","title":"Saved filters survive export","detail":"Exporting a filtered report keeps the filter.","pulls":[12],"issues":[9]},{"kind":"feature","title":"CSV export","detail":"Reports can be downloaded as CSV.","pulls":[14,12]}]}`
	got, err := parseBulletin(text, []int{12, 14}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Faster exports" || len(got.Items) != 2 || got.Items[1].Pulls[0] != 12 || got.Items[0].Issues[0] != 9 {
		t.Fatalf("unexpected bulletin %+v", got)
	}
	if _, err := parseBulletin(text, []int{12}, 10); err == nil || !strings.Contains(err.Error(), "#14 outside the window") {
		t.Fatalf("a pull request outside the window was accepted: %v", err)
	}
	if _, err := parseBulletin(text, []int{12, 14}, 1); err == nil {
		t.Fatal("more items than max_items were accepted")
	}
	for name, bad := range map[string]string{
		"unknown kind":  `MAYOR_BULLETIN {"title":"t","summary":"s","items":[{"kind":"refactor","title":"x","detail":"y","pulls":[12]}]}`,
		"no pulls":      `MAYOR_BULLETIN {"title":"t","summary":"s","items":[{"kind":"fix","title":"x","detail":"y","pulls":[]}]}`,
		"missing items": `MAYOR_BULLETIN {"title":"t","summary":"s"}`,
		"empty summary": `MAYOR_BULLETIN {"title":"t","summary":"","items":[]}`,
		"invalid issue": `MAYOR_BULLETIN {"title":"t","summary":"s","items":[{"kind":"fix","title":"x","detail":"y","pulls":[12],"issues":[0]}]}`,
	} {
		if _, err := parseBulletin(bad, []int{12}, 10); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	empty, err := parseBulletin(`MAYOR_BULLETIN {"title":"Quiet","summary":"Only internal work merged.","items":[]}`, []int{12}, 10)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("an empty items array is valid: %v", err)
	}
}

func TestLinkedIssuesReadClosingKeywordsOnly(t *testing.T) {
	got := linkedIssues("Fixes #12, closes: #7 and mentions #99. Resolved #12 again.\nfix #3")
	if len(got) != 3 || got[0] != 3 || got[1] != 7 || got[2] != 12 {
		t.Fatalf("linked issues = %v", got)
	}
}

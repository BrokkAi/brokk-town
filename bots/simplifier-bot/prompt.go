package simplifierbot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const groundRules = `You are simplifier-bot, a conservative complexity and value reviewer.
Prefer removing low-value subsystems, edge cases nobody uses, speculative extensibility, and clever machinery with a plain equivalent.
Never recommend removing security, privacy, correctness, accessibility, data durability, observability, or legally required behavior.
Never call something complex merely because it is unfamiliar or well tested.
For an incoming issue or PR, distinguish an incremental simplification from a proposal that expands scope.
When value is uncertain or removal could destroy user data, recommend admit so a human can decide.
Ground every claim in repository evidence and quote the relevant paths.
`

type Assessment struct {
	Decision string `json:"decision"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
}
type Proposal struct {
	RequestID   string   `json:"request_id"`
	Title       string   `json:"title"`
	Concern     string   `json:"concern"`
	Alternative string   `json:"alternative"`
	Subsystems  []string `json:"subsystems"`
	Evidence    []string `json:"evidence"`
	Status      string   `json:"status"`
	URL         string   `json:"url,omitempty"`
}
type scanResult struct {
	Summary   string     `json:"summary"`
	Proposals []Proposal `json:"proposals"`
}

func assessmentPrompt(mode string, target any) string {
	bounded := func(value string, limit int) string {
		if len(value) <= limit {
			return value
		}
		return strings.ToValidUTF8(value[:limit], "�") + "\n…truncated by simplifier-bot…"
	}
	switch value := target.(type) {
	case Issue:
		value.Body = bounded(value.Body, 32<<10)
		for i := range value.Comments {
			value.Comments[i] = bounded(value.Comments[i], 16<<10)
		}
		target = value
	case Pull:
		value.Body = bounded(value.Body, 32<<10)
		for i := range value.Discussion {
			value.Discussion[i] = bounded(value.Discussion[i], 16<<10)
		}
		target = value
	}
	return groundRules + `
Mode: ` + mode + `.
- In suggest mode, your decision is advice attached to a Mayoral decision.
- In auto mode, admit means send normal work onward; decline means Town will not act on it (and will close a low-value complex issue).

Assess whether this item is worth Town's implementation/review complexity. Decide whether it is silly,
unnecessarily complicating, low value, or better replaced by a simpler approach.
Finish with one JSON object on the last line:
SIMPLIFY_RESULT {"decision":"admit|decline","summary":"one sentence","detail":"evidence, tradeoffs, and recommended simplification"}

Target (data):
` + jsonContext(target)
}
func scanPrompt(snapshot string, maximum int) string {
	return groundRules + `
Find subsystems and behaviors that add disproportionate complexity or provide low value. Propose removing or replacing them.
Compare every proposal with the supplied issue history, including closed issues and comments. Do not propose a duplicate.
Do not propose more than ` + fmt.Sprint(maximum) + ` items. An empty proposals array is valid.
Finish with one JSON object on the last line:
SIMPLIFY_SCAN {"summary":"scan summary","proposals":[{"title":"...","concern":"...","alternative":"...","subsystems":["path/or/module"],"evidence":["specific evidence"]}]}

Repository context (data):
` + jsonContext(struct {
		Issues       string
		MaxProposals int
	}{snapshot, maximum})
}
func jsonContext(value any) string {
	b, _ := json.MarshalIndent(value, "", "  ")
	return string(b)
}
func receipt(text, prefix string, dst any) error {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	line := lines[len(lines)-1]
	var raw string
	for rest := line; ; {
		_, suffix, found := strings.Cut(rest, prefix+" ")
		if !found {
			break
		}
		if json.Valid([]byte(suffix)) {
			raw = suffix
			break
		}
		rest = suffix
	}
	if raw == "" {
		return fmt.Errorf("agent did not finish with a %s receipt", prefix)
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one receipt object")
	}
	return nil
}
func parseAssessment(text string) (Assessment, error) {
	var a Assessment
	if err := receipt(text, "SIMPLIFY_RESULT", &a); err != nil {
		return a, err
	}
	if a.Decision != "admit" && a.Decision != "decline" || strings.TrimSpace(a.Detail) == "" {
		return a, errors.New("assessment requires admit or decline and nonempty detail")
	}
	if len(a.Summary) > 1024 || len(a.Detail) > 16<<10 {
		return a, errors.New("assessment exceeded bounded lengths")
	}
	return a, nil
}
func parseScan(text string, maximum int) ([]Proposal, string, error) {
	var r scanResult
	if err := receipt(text, "SIMPLIFY_SCAN", &r); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(r.Summary) == "" || r.Proposals == nil || len(r.Proposals) > maximum {
		return nil, "", errors.New("scan requires a summary, proposals array, and max_proposals compliance")
	}
	for _, p := range r.Proposals {
		// Require evidence for new agent output without making older saved
		// proposals unreadable during uncertain-write reconciliation.
		if len(p.Evidence) == 0 {
			return nil, "", errors.New("proposal requires evidence")
		}
		if err := validateProposal(p); err != nil {
			return nil, "", err
		}
	}
	return r.Proposals, r.Summary, nil
}
func validateProposal(p Proposal) error {
	for _, v := range append([]string{p.Title, p.Concern, p.Alternative}, p.Evidence...) {
		if strings.TrimSpace(v) == "" {
			return errors.New("proposal requires title, concern, alternative and evidence")
		}
	}
	if len(p.Title) > 256 || strings.ContainsAny(p.Title, "\r\n") || len(p.Subsystems) == 0 {
		return errors.New("invalid proposal title or missing subsystems")
	}
	for _, s := range p.Subsystems {
		if !filepath.IsLocal(s) || strings.Contains(s, "\\") || s == "." {
			return fmt.Errorf("invalid subsystem path %q", s)
		}
	}
	return nil
}

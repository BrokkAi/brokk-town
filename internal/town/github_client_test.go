package town

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ghRoute answers every gh invocation whose argv, joined by spaces, starts with
// prefix. Routes are tried in order, so a more specific prefix goes first.
type ghRoute struct {
	prefix string
	stdout string
	stderr string
	code   int
}

// recordingGH is a fake gh on PATH: it records each argv, and the JSON body of
// every --input file, and answers from canned routes. An unrouted call fails,
// so no test can reach a real GitHub by accident.
type recordingGH struct {
	dir string
}

func fakeGitHubCLI(t *testing.T, routes ...ghRoute) recordingGH {
	t.Helper()
	dir := t.TempDir()
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString("printf '%s\\n' \"$*\" >> '" + dir + "/argv'\n")
	script.WriteString("prev=\nfor a in \"$@\"; do if [ \"$prev\" = --input ]; then cat \"$a\" >> '" + dir + "/bodies'; fi; prev=$a; done\n")
	script.WriteString("case \"$*\" in\n")
	for i, r := range routes {
		out := filepath.Join(dir, fmt.Sprintf("out%d", i))
		errOut := filepath.Join(dir, fmt.Sprintf("err%d", i))
		if err := os.WriteFile(out, []byte(r.stdout), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(errOut, []byte(r.stderr), 0o600); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(r.prefix, "'\"\\$`") {
			t.Fatalf("route prefix %q needs quoting", r.prefix)
		}
		fmt.Fprintf(&script, "  \"%s\"*) cat '%s'; cat '%s' >&2; exit %d;;\n", r.prefix, out, errOut, r.code)
	}
	script.WriteString("  *) echo \"unrouted gh call: $*\" >&2; exit 97;;\nesac\n")
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return recordingGH{dir}
}

func (r recordingGH) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, "argv"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// bodies decodes every request body gh was given, in order.
func (r recordingGH) bodies(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, "bodies"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var v map[string]any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

const apiPrefix = "api --hostname github.com --method "

func jsonList(n int, item func(int) string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = item(i)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestPullAndActorReadTheExactEndpoint(t *testing.T) {
	gh := fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "GET repos/o/r/pulls/7", stdout: `{"number":7,"state":"open","draft":true,"head":{"ref":"issue-7","sha":"` + headSHA + `","repo":{"full_name":"o/r"}},"base":{"ref":"main","sha":"` + baseSHA + `"},"user":{"login":"bot"}}`},
		ghRoute{prefix: apiPrefix + "GET user", stdout: `{"login":"town-bot"}`},
	)
	ctx := context.Background()
	p, err := GitHubClient{}.Pull(ctx, "o/r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if p.Number != 7 || !p.Draft || p.Head.Ref != "issue-7" || p.Head.SHA != headSHA || p.Head.Repo.FullName != "o/r" || p.Base.SHA != baseSHA || p.User.Login != "bot" {
		t.Fatalf("pull decoded wrongly: %+v", p)
	}
	actor, err := GitHubClient{}.Actor(ctx)
	if err != nil || actor != "town-bot" {
		t.Fatalf("actor = %q, %v", actor, err)
	}
	want := []string{apiPrefix + "GET repos/o/r/pulls/7", apiPrefix + "GET user"}
	if got := gh.calls(t); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gh argv = %q", got)
	}
	if gh.bodies(t) != nil {
		t.Fatal("a read sent a request body")
	}
}

func TestAPIFailuresAndMalformedJSONAreErrors(t *testing.T) {
	fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "GET repos/o/r/pulls/1", stderr: "HTTP 502", code: 1},
		ghRoute{prefix: apiPrefix + "GET repos/o/r/pulls/2", stdout: `{"number":`},
	)
	if _, err := (GitHubClient{}).Pull(context.Background(), "o/r", 1); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("failed gh call = %v", err)
	}
	if _, err := (GitHubClient{}).Pull(context.Background(), "o/r", 2); err == nil {
		t.Fatal("truncated JSON decoded as a pull request")
	}
}

// Discussion reads every page of all three sources and keeps each item's
// source in its ID, so an inline comment and a review with the same numeric ID
// never collide.
func TestDiscussionReadsEveryPageOfEverySource(t *testing.T) {
	full := jsonList(100, func(i int) string { return fmt.Sprintf(`{"id":%d,"body":"c%d"}`, i+1, i+1) })
	gh := fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "GET repos/o/r/issues/3/comments?per_page=100&page=1", stdout: full},
		ghRoute{prefix: apiPrefix + "GET repos/o/r/issues/3/comments?per_page=100&page=2", stdout: `[{"id":101,"body":"last"}]`},
		ghRoute{prefix: apiPrefix + "GET repos/o/r/pulls/3/comments?per_page=100&page=1", stdout: `[{"id":5,"body":"inline","path":"a.go"}]`},
		ghRoute{prefix: apiPrefix + "GET repos/o/r/pulls/3/reviews?per_page=100&page=1", stdout: `[{"id":5,"body":"lgtm","state":"APPROVED"}]`},
	)
	d, err := GitHubClient{}.Discussion(context.Background(), "o/r", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 103 {
		t.Fatalf("discussion has %d items, want 103", len(d))
	}
	if d[0] != (Discussion{ID: "comment:1", Body: "c1"}) || d[100] != (Discussion{ID: "comment:101", Body: "last"}) {
		t.Fatalf("issue comments decoded wrongly: %+v %+v", d[0], d[100])
	}
	if d[101] != (Discussion{ID: "inline:5", Body: "inline", Path: "a.go"}) || d[102] != (Discussion{ID: "review:5", Body: "lgtm", State: "APPROVED"}) {
		t.Fatalf("inline and review items decoded wrongly: %+v %+v", d[101], d[102])
	}
	if n := len(gh.calls(t)); n != 4 {
		t.Fatalf("gh called %d times, want 4 (a short page ends pagination)", n)
	}
}

// A failure on any page fails the whole read: a partial discussion would let a
// review certify against feedback it never saw.
func TestDiscussionRefusesAPartialRead(t *testing.T) {
	full := jsonList(100, func(i int) string { return fmt.Sprintf(`{"id":%d}`, i+1) })
	fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "GET repos/o/r/issues/3/comments?per_page=100&page=1", stdout: full},
		ghRoute{prefix: apiPrefix + "GET repos/o/r/issues/3/comments?per_page=100&page=2", stderr: "rate limited", code: 1},
	)
	if d, err := (GitHubClient{}).Discussion(context.Background(), "o/r", 3); err == nil || d != nil {
		t.Fatalf("partial discussion returned: %d items, %v", len(d), err)
	}
}

// A path that already carries a query string continues it rather than
// starting a second one.
func TestFindRequestPaginatesAQueryPath(t *testing.T) {
	marker := requestMarker("req-1")
	gh := fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "GET repos/o/r/issues?state=all&sort=created&direction=desc&per_page=100&page=1", stdout: `[{"number":4,"body":"` + marker + `","pull_request":{"url":"x"}},{"number":5,"body":"filed ` + marker + `"},{"number":6,"body":"other"}]`},
	)
	found, err := GitHubClient{}.FindRequest(context.Background(), "o/r", "req-1")
	if err != nil || found == nil || found.Number != 5 {
		t.Fatalf("found = %+v, %v", found, err)
	}
	if calls := gh.calls(t); len(calls) != 1 {
		t.Fatalf("calls = %q", calls)
	}
}

func TestGateParsesEveryMergeField(t *testing.T) {
	gh := fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "POST graphql --input ", stdout: `{"data":{"repository":{"squashMergeAllowed":true,"pullRequest":{"headRefOid":"` + headSHA + `","baseRefOid":"` + baseSHA + `","baseRefName":"main","isDraft":false,"state":"OPEN","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","mergeQueue":null,"statusCheckRollup":{"state":"SUCCESS"}}}}}`})
	gate, err := GitHubClient{}.Gate(context.Background(), "o/r", 9)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Head != headSHA || gate.Base != baseSHA || gate.BaseRef != "main" || !gate.PolicyKnown || !gate.SquashAllowed || gate.Checks == nil || gate.Checks.State != "SUCCESS" || !gate.Allows(pull(9), clean(), "main") {
		t.Fatalf("gate = %+v", gate)
	}
	bodies := gh.bodies(t)
	if len(bodies) != 1 {
		t.Fatal(bodies)
	}
	query, _ := bodies[0]["query"].(string)
	for _, field := range []string{"headRefOid", "baseRefOid", "baseRefName", "isDraft", "state", "mergeable", "mergeStateStatus", "reviewDecision", "mergeQueue", "squashMergeAllowed", "statusCheckRollup"} {
		if !strings.Contains(query, field) {
			t.Fatal("missing gate field", field)
		}
	}
	variables := bodies[0]["variables"].(map[string]any)
	if variables["owner"] != "o" || variables["name"] != "r" || variables["number"] != float64(9) {
		t.Fatal(variables)
	}
}

func TestGateRejectsIncompleteOutput(t *testing.T) {
	for _, reply := range []string{"not json", `{}`, `{"data":{"repository":null}}`, `{"data":{"repository":{"squashMergeAllowed":true,"pullRequest":null}}}`, `{"data":{"repository":{"squashMergeAllowed":true,"pullRequest":{}}}}`, `{"errors":[{"message":"secret"}]}`} {
		t.Run(reply, func(t *testing.T) {
			fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "POST graphql --input ", stdout: reply})
			gate, err := GitHubClient{}.Gate(context.Background(), "o/r", 9)
			if err == nil || gate.Allows(pull(9), clean(), "main") {
				t.Fatal("accepted incomplete gate", gate, err)
			}
		})
	}
}

// Merge pins the exact reviewed head and reports success only when GitHub
// confirms it with a merge commit.
func TestMergeRequiresGitHubsConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, reply, want string
		ok                bool
	}{
		{"merged", `{"merged":true,"sha":"` + fixSHA + `"}`, fixSHA, true},
		{"not_merged", `{"merged":false,"message":"Head branch was modified"}`, "Head branch was modified", false},
		{"merged_without_commit", `{"merged":true,"sha":"nope"}`, "merge not confirmed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "PUT repos/o/r/pulls/2/merge --input ", stdout: tc.reply})
			sha, err := GitHubClient{}.Merge(context.Background(), "o/r", 2, headSHA)
			if tc.ok {
				if err != nil || sha != tc.want {
					t.Fatalf("merge = %q, %v", sha, err)
				}
			} else if err == nil || sha != "" || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unconfirmed merge = %q, %v", sha, err)
			}
			bodies := gh.bodies(t)
			if len(bodies) != 1 || bodies[0]["sha"] != headSHA || bodies[0]["merge_method"] != "squash" {
				t.Fatalf("merge body = %v", bodies)
			}
		})
	}
}

func TestMergeFailureIsNotAMerge(t *testing.T) {
	fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "PUT ", stderr: "HTTP 405: Required status check is expected", code: 1})
	if sha, err := (GitHubClient{}).Merge(context.Background(), "o/r", 2, headSHA); err == nil || sha != "" {
		t.Fatalf("rejected merge = %q, %v", sha, err)
	}
}

func TestWritesSendTheirMethodPathAndBody(t *testing.T) {
	gh := fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "PATCH repos/o/r/issues/4 --input "},
		ghRoute{prefix: apiPrefix + "PATCH repos/o/r/pulls/5 --input "},
		ghRoute{prefix: apiPrefix + "POST repos/o/r/issues/6/comments --input "},
		ghRoute{prefix: apiPrefix + "POST repos/o/r/issues --input ", stdout: `{"number":8,"title":"T","html_url":"https://example.test/8","state":"open"}`},
	)
	ctx := context.Background()
	g := GitHubClient{}
	if err := g.CloseIssue(ctx, "o/r", 4); err != nil {
		t.Fatal(err)
	}
	if err := g.ClosePull(ctx, "o/r", 5); err != nil {
		t.Fatal(err)
	}
	if err := g.Comment(ctx, "o/r", 6, "hello"); err != nil {
		t.Fatal(err)
	}
	issue, err := g.CreateIssue(ctx, "o/r", "T", "B")
	if err != nil || issue.Number != 8 || issue.URL != "https://example.test/8" {
		t.Fatalf("created issue = %+v, %v", issue, err)
	}
	want := []map[string]any{
		{"state": "closed", "state_reason": "not_planned"},
		{"state": "closed"},
		{"body": "hello"},
		{"title": "T", "body": "B"},
	}
	got := gh.bodies(t)
	if len(got) != len(want) {
		t.Fatalf("bodies = %v", got)
	}
	for i := range want {
		if fmt.Sprint(got[i]) != fmt.Sprint(want[i]) {
			t.Fatalf("body %d = %v, want %v", i, got[i], want[i])
		}
	}
	if n := len(gh.calls(t)); n != 4 {
		t.Fatalf("gh called %d times", n)
	}
}

func TestDeleteBranch(t *testing.T) {
	gh := fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "DELETE repos/o/r/git/refs/heads/issue-1"},
		ghRoute{prefix: apiPrefix + "DELETE repos/o/r/git/refs/heads/issue-2", stderr: "gh: Reference does not exist (HTTP 422)", code: 1},
		ghRoute{prefix: apiPrefix + "DELETE repos/o/r/git/refs/heads/issue-3", stderr: "HTTP 403", code: 1},
	)
	ctx := context.Background()
	g := GitHubClient{}
	if err := g.DeleteBranch(ctx, "o/r", "issue-1"); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteBranch(ctx, "o/r", "issue-2"); err != nil {
		t.Fatalf("an already deleted branch is not a failure: %v", err)
	}
	if err := g.DeleteBranch(ctx, "o/r", "issue-3"); err == nil {
		t.Fatal("a refused delete reported success")
	}
	for _, bad := range []string{"", "a..b", "x.lock", "../main"} {
		if err := g.DeleteBranch(ctx, "o/r", bad); err == nil {
			t.Fatalf("invalid branch %q reached GitHub", bad)
		}
	}
	if n := len(gh.calls(t)); n != 3 {
		t.Fatalf("gh called %d times, want 3", n)
	}
}

// Contains trusts only the compare API's ancestry status.
func TestContainsReadsAncestryFromCompare(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{{"ahead", true}, {"identical", true}, {"behind", false}, {"diverged", false}} {
		t.Run(tc.status, func(t *testing.T) {
			gh := fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "GET repos/o/r/compare/" + headSHA + "...v1.0%2Frc", stdout: `{"status":"` + tc.status + `"}`})
			got, err := GitHubClient{}.Contains(context.Background(), "o/r", headSHA, "v1.0/rc")
			if err != nil || got != tc.want {
				t.Fatalf("contains = %v, %v; calls %q", got, err, gh.calls(t))
			}
		})
	}
}

func TestContainsRefusesBadQueriesAndFailures(t *testing.T) {
	gh := fakeGitHubCLI(t, ghRoute{prefix: apiPrefix + "GET repos/o/r/compare/", stderr: "HTTP 404", code: 1})
	ctx := context.Background()
	for _, q := range [][2]string{{"short", "v1"}, {headSHA, ""}} {
		if ok, err := (GitHubClient{}).Contains(ctx, "o/r", q[0], q[1]); ok || err == nil {
			t.Fatalf("invalid query %q answered %v, %v", q, ok, err)
		}
	}
	if len(gh.calls(t)) != 0 {
		t.Fatal("an invalid ancestry query reached GitHub")
	}
	if ok, err := (GitHubClient{}).Contains(ctx, "o/r", headSHA, "v1"); ok || err == nil {
		t.Fatalf("failed compare answered %v, %v", ok, err)
	}
}

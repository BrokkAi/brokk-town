package town

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issues, comments and replies written before the rename to SlopCop Squad carry
// "<!-- brokk-town" markers. Every lookup for an earlier write still finds
// them, so an upgrade in the middle of a write does not repeat it.

func TestHasMarkerAcceptsPreRenameSpelling(t *testing.T) {
	task := &Task{Number: 5, Closes: 1}
	for _, test := range []struct {
		text, marker string
		want         bool
	}{
		{"filed\n\n<!-- brokk-town-request:req-1 -->", requestMarker("req-1"), true},
		{"filed\n\n" + requestMarker("req-1"), requestMarker("req-1"), true},
		{"filed\n\n<!-- brokk-town-request:req-2 -->", requestMarker("req-1"), false},
		{"## Brokk Town\n\n<!-- brokk-town:requeued pr=5 close=1 -->", requeueMarker(task), true},
		{"## Brokk Town\n\n<!-- brokk-town:closed-after-review pr=5 close=1 -->", closeMarker(task), true},
		{"<!-- brokk-town:closed-after-review pr=5 close=2 -->", closeMarker(task), false},
		{"<!-- brokk-town:requeued pr=5 close=1 -->", closeMarker(task), false},
	} {
		if got := hasMarker(test.text, test.marker); got != test.want {
			t.Errorf("hasMarker(%q, %q) = %v, want %v", test.text, test.marker, got, test.want)
		}
	}
}

func TestRequestBodyCannotCarryEitherRequestMarker(t *testing.T) {
	for _, marker := range []string{"<!-- slopcop-squad-request:x -->", "<!-- brokk-town-request:x -->"} {
		r := sampleRequest()
		r.Body += "\n" + marker
		if err := r.validate(); err == nil {
			t.Errorf("body carrying %q was accepted", marker)
		}
	}
}

func TestFindRequestRecognisesPreRenameMarker(t *testing.T) {
	fakeGitHubCLI(t,
		ghRoute{prefix: apiPrefix + "GET repos/o/r/issues?state=all&sort=created&direction=desc&per_page=100&page=1", stdout: `[{"number":5,"body":"filed <!-- brokk-town-request:req-1 -->"},{"number":6,"body":"other"}]`},
	)
	found, err := GitHubClient{}.FindRequest(context.Background(), "o/r", "req-1")
	if err != nil || found == nil || found.Number != 5 {
		t.Fatalf("found = %+v, %v", found, err)
	}
}

// Brokk Town filed the issue and lost the response; SlopCop Squad confirms it
// rather than filing it again.
func TestRequestFiledBeforeRenameIsConfirmed(t *testing.T) {
	s := testStore(t, false)
	x := addTown(t, s)
	sup := NewSupervisor(s, nil, nil)
	posts := 0
	var remote RemoteIssue
	sup.Publisher = requestPublisher{post: func(_ context.Context, repo, title, _ string) (RemoteIssue, error) {
		posts++
		body := "## Feature request\n\n" + sampleRequest().Body + "\n\n<!-- brokk-town-request:" + sampleRequest().ID + " -->"
		remote = RemoteIssue{Number: 51, Title: title, Body: body, URL: "https://github.com/" + repo + "/issues/51", State: "open"}
		return RemoteIssue{}, errors.New("response lost after creation")
	}, find: func(context.Context, string, string) (*RemoteIssue, error) { return &remote, nil }}
	if _, err := sup.SubmitRequest(x.ID, sampleRequest()); err != nil {
		t.Fatal(err)
	}
	sup.publishRequest(context.Background(), x.ID, sampleRequest().ID)
	sup.publishRequest(context.Background(), x.ID, sampleRequest().ID)
	if r := s.Snapshot().Towns[x.ID].Requests[sampleRequest().ID]; posts != 1 || r.Status != "confirmed" {
		t.Fatalf("pre-rename issue was not confirmed: posts=%d %+v", posts, r)
	}
}

func TestPreRenameRequeueCommentIsNotRepeated(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	gh.comments = []string{"#3: earlier\n\n<!-- brokk-town:requeued pr=5 close=1 -->"}
	if err := sup.reconcileNow(context.Background(), store.Snapshot().Towns[town.ID]); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 2 || !strings.HasPrefix(gh.comments[1], "#5: ") {
		t.Fatalf("requeue comment repeated: %v", gh.comments)
	}
}

func TestPreRenameClosingCommentIsNotRepeated(t *testing.T) {
	store, town, gh, sup := ownPullTown(t, "auto")
	declineOwnPull(t, store, town)
	update(t, store, func(st *State) { claimDeclinedPulls(st, st.Towns[town.ID], time.Now()) })
	posted := strings.Replace(closingComment(task(store, town, "pr:5"), 3), "<!-- slopcop-squad:", "<!-- brokk-town:", 1)
	gh.comments = []string{"#5: " + posted}
	gh.p.State = "closed"
	gh.snapshot.Pulls = []Pull{gh.p}
	if err := sup.closeRetiredPulls(context.Background(), store.Snapshot().Towns[town.ID], gh.snapshot); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 2 || !strings.HasPrefix(gh.comments[1], "#3: ") {
		t.Fatalf("closing comment repeated: %v", gh.comments)
	}
}

func TestSlackReconcileFindsPreRenameReply(t *testing.T) {
	marker := MarkerForOperation("event-42")
	legacy := "<!-- brokk-town-slack:" + strings.TrimPrefix(marker, "<!-- slopcop-squad-slack:")
	fake := &slackFakeHTTP{}
	fake.do = func(req *http.Request, _ url.Values) (*http.Response, error) {
		if req.URL.Path != "/api/conversations.replies" {
			t.Fatalf("unexpected Slack method %s", req.URL.Path)
		}
		body := map[string]any{"ok": true, "messages": []SlackMessage{{TS: "1.000", Text: "source"}, {TS: "2.000", ThreadTS: "1.000", Text: "completed\n" + legacy}}, "has_more": false}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(slackJSON(t, body))), Request: req}, nil
	}
	funnel := slackFunnelForTest(fake, SlackFunnelOptions{})
	write := SlackWrite{Kind: SlackThreadWrite, Status: SlackWriteUncertain, Channel: "C123", MessageTS: "1.000", Marker: marker, OperationID: "event-42"}
	receipt, err := funnel.ReconcileWrite(context.Background(), write)
	if err != nil || !receipt.Found || receipt.Write.Status != SlackWriteConfirmed {
		t.Fatalf("pre-rename reply not found: %+v, %v", receipt, err)
	}
}

func TestSlackMarkerKeepsPreRenameMarker(t *testing.T) {
	legacy := "<!-- brokk-town-slack:0123456789abcdef01234567 -->"
	if got := slackMarker(legacy, ""); got != legacy {
		t.Fatalf("slackMarker(%q) = %q", legacy, got)
	}
}

//go:build mjolnir_acceptance

package town

// This is a separately invoked, opt-in real-target acceptance demonstration.
// Normal development tests and CI exclude it. It reads an existing real PR and
// sends only dry-run work to Review Bot; it never invokes Town's publisher or
// merge path. Failure preserves its dedicated state and remote sessions.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
	"github.com/BrokkAi/brokk-town/internal/osrun"
)

func TestMjolnirReadOnlyAcceptance(t *testing.T) {
	path := os.Getenv("BT_MJOLNIR_ACCEPTANCE")
	if path == "" {
		t.Skip("requires an explicit private acceptance configuration")
	}
	var cfg struct {
		Root, Repo, Target, Profile, Discovery, ReviewBinary string
		PR                                                   int
		Command                                              []string
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &cfg) != nil || !filepath.IsAbs(cfg.Root) || !filepath.IsAbs(cfg.ReviewBinary) || len(cfg.Command) == 0 || cfg.PR < 1 {
		t.Fatal("invalid acceptance configuration")
	}
	// Never reuse a prior attempt's state or remove it on a failed run.
	if err := os.Mkdir(cfg.Root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Minute)
	defer cancel()
	connection := mjolnir.Environment()
	catalog := mjolnir.New(cfg.Root, false, connection)
	selection := mjolnir.Selection{Target: cfg.Target, Profile: cfg.Profile}
	pin, err := catalog.DiscoverRuntime(ctx, selection, cfg.Discovery)
	if err != nil {
		t.Fatal(err)
	}
	pull, err := (GitHubClient{}).Pull(ctx, cfg.Repo, cfg.PR)
	if err != nil || pull.State != "open" || pull.Draft || !SHA(pull.Base.SHA) || !SHA(pull.Head.SHA) {
		t.Fatal("acceptance requires an existing open, non-draft PR with exact revisions")
	}
	x := &Town{ID: strings.ToLower(cfg.Repo), Config: DefaultConfig(cfg.Repo)}
	x.Config.Execution, x.Config.Branch = &selection, pull.Base.Ref
	x.Config.ExecutionRuntimes = map[string]mjolnir.RuntimePin{runtimeSelectionKey(selection): pin}
	b := &BotWorkers{Root: cfg.Root, Mjolnir: catalog, MjolnirCommand: cfg.Command, botCommands: map[Role]string{Review: cfg.ReviewBinary}}
	observe := func(p Progress) { t.Log("worker phase:", p.Phase) }
	managed, err := b.beginManaged(ctx, x, Review, observe)
	if err != nil {
		t.Fatal(err)
	}
	socket, close, err := startRemoteAgent(ctx, managed, pull.Head.SHA)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	bot, err := b.workerBot(ctx, x.ID, Review)
	if err != nil {
		t.Fatal(err)
	}
	process, err := startWorkerProcess(ctx, bot)
	if err != nil {
		t.Fatal(err)
	}
	defer process.close()
	dir, state := Workspace(cfg.Root, x.ID, Review)
	request := workerRequest{Protocol: 1, Remote: "https://github.com/" + cfg.Repo + ".git", Branch: pull.Base.Ref,
		Directory: dir, StateDirectory: state, Repo: cfg.Repo, Host: "github.com", PR: cfg.PR,
		BaseSHA: pull.Base.SHA, HeadSHA: pull.Head.SHA, RemoteAgent: socket, DryRun: true}
	result, err := process.run(ctx, request, false, time.Now().Add(45*time.Minute), observe, nil)
	if err != nil {
		t.Fatalf("%v (managed cause: %v)", err, managed.failure())
	}
	managed.mu.Lock()
	runs := append([]*mjolnir.Run{}, managed.runs...)
	managed.mu.Unlock()
	if result.Review == nil || result.Review.Status != "dry_run" || result.Review.Complete || result.Review.ExactHead != pull.Head.SHA || result.Review.ExactBase != pull.Base.SHA || len(runs) == 0 {
		t.Fatal("worker did not confirm an exact-revision dry-run review")
	}
	for _, run := range runs {
		record := run.Record()
		if record.State != "evidence" || record.Evidence == "" || record.Plan.Checkout.Commit != pull.Head.SHA || record.Receipt.Runtime.ID != pin.Runtime.ID {
			t.Fatal("remote run lacks guarded exact-revision evidence")
		}
		command := append(append([]string{}, cfg.Command...), "sessions", "--session", record.Session, "--json")
		text, err := osrun.Run(ctx, "", nil, command...)
		var visible struct {
			ID string `json:"id"`
		}
		if err != nil || json.Unmarshal([]byte(text), &visible) != nil || visible.ID != record.Session {
			t.Fatal("accepted session is not visible through mj sessions")
		}
		awaitAcceptanceIndex(t, ctx, connection, record.Session, selection)
		t.Log("validated and indexed session:", record.Session, "evidence:", record.Evidence)
	}
	records, err := managed.executor.Runs.Records()
	if err != nil {
		t.Fatal(err)
	}
	proof, _ := json.MarshalIndent(struct {
		Repo       string `json:"repo"`
		PR         int    `json:"pr"`
		Base, Head string
		DryRun     bool                `json:"dry_run"`
		Runs       []mjolnir.RunRecord `json:"runs"`
	}{cfg.Repo, cfg.PR, pull.Base.SHA, pull.Head.SHA, true, records}, "", "  ")
	if err := os.WriteFile(filepath.Join(cfg.Root, "acceptance.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
	// Validate the production cleanup path only after CLI and index visibility
	// have been observed and evidence has been retained by the run lifecycle.
	if err := managed.finish(ctx); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.Record().State != "destroyed" {
			t.Fatal("cleanup was not confirmed")
		}
	}
	t.Log("dry-run acceptance and confirmed cleanup passed; private proof:", filepath.Join(cfg.Root, "acceptance.json"))
}

func awaitAcceptanceIndex(t *testing.T, ctx context.Context, connection mjolnir.Connection, session string, selection mjolnir.Selection) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		token, err := os.ReadFile(connection.TokenFile)
		if err != nil {
			t.Fatal("cannot read acceptance API token")
		}
		// Point lookups only read the existing index. Search is the documented
		// surface that starts a background refresh when its snapshot is stale.
		refresh, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(connection.URL, "/")+"/wiki/search?q="+url.QueryEscape(session)+"&limit=1", nil)
		if err != nil {
			t.Fatal("invalid acceptance index URL")
		}
		refresh.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		if response, err := client.Do(refresh); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
		}
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(connection.URL, "/")+"/wiki/sessions/"+session, nil)
		if err != nil {
			t.Fatal("invalid acceptance API URL")
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		res, err := client.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
			res.Body.Close()
			var entry struct {
				ID      string `json:"wiki_id"`
				Session string `json:"mjolnir_session_id"`
				Target  string `json:"target_template_id"`
				Profile string `json:"profile_id"`
			}
			if res.StatusCode == 200 && res.Header.Get("Mj-Api-Version") == "1" && readErr == nil && len(data) <= 1<<20 && json.Unmarshal(data, &entry) == nil && entry.ID == session && entry.Session == session && entry.Target == selection.Target && entry.Profile == selection.Profile {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("accepted session did not appear in the Mjolnir index")
		case <-time.After(time.Second):
		}
	}
}

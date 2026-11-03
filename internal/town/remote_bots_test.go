package town

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/botapi/botclient"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

const remoteToken = "0123456789abcdef0123456789abcdef"

// remoteBot serves bot over HTTP and returns a client for it, as Town reaches
// a bot in another container.
func remoteBot(t *testing.T, bot botapi.Bot, role Role, version string, demo bool) *botclient.Client {
	t.Helper()
	server := &botserver.Server{Bot: bot, Identity: botapi.Identity{Name: BotName(role), Role: string(role), Version: version, Contract: botapi.Version, Demo: demo}, Token: remoteToken}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	client, err := botclient.New(ts.URL, remoteToken, BotName(role), string(role), "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func simplifierTown(t *testing.T, demo bool) (*Store, *Town) {
	store := testStore(t, demo)
	x := addTown(t, store)
	update(t, store, func(st *State) {
		st.Towns[x.ID].Tasks["issue:7"] = &Task{ID: "issue:7", Kind: "issue", Number: 7, Title: "Complex request", Stage: "simplifying", House: Simplifier, Updated: time.Now()}
	})
	return store, store.Snapshot().Towns[x.ID]
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A dispatch over the network records the version the bot reports before the
// work is sent, and carries the bot's result back.
func TestRemoteDispatchRecordsTheBotsVersion(t *testing.T) {
	store, x := simplifierTown(t, false)
	var recorded string
	bot := &recordingBot{result: botapi.Result{Simplification: &botapi.Simplification{Mode: "suggest", Decision: "decline", Detail: "No callers."}}}
	observing := botFunc(func(ctx context.Context, r botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
		if run := store.Snapshot().Towns[x.ID].Workers[Simplifier].Run; run != nil {
			recorded = run.Version
		}
		return bot.Run(ctx, r, emit)
	})
	workers := &BotWorkers{Root: t.TempDir(), Store: store, Bots: map[Role]botapi.Bot{Simplifier: remoteBot(t, observing, Simplifier, "v1.4.2", false)}}
	result, err := workers.Run(context.Background(), x, Simplifier, func(Progress) {}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if recorded != "v1.4.2" {
		t.Fatalf("recorded version %q", recorded)
	}
	if result.Simplification == nil || result.Simplification.Decision != "decline" || bot.request.Issue != 7 {
		t.Fatalf("result %+v, request %+v", result, bot.request)
	}
	if recordedVersion("dev") != "0.0.0-dev" || recordedVersion("v1.2.3") != "v1.2.3" || !workerVersionPattern.MatchString(recordedVersion("weird version/!")) {
		t.Fatal("recorded versions are not storable")
	}
}

type botFunc func(context.Context, botapi.Request, func(botapi.Event)) (botapi.Result, error)

func (f botFunc) Run(ctx context.Context, r botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
	return f(ctx, r, emit)
}
func (botFunc) Storage(context.Context, botapi.StorageRequest) (botapi.StorageResult, error) {
	return botapi.StorageResult{}, nil
}

// A demo bot never answers a real service, and a real bot never answers the
// demo. Neither is sent the work.
func TestDemoAndRealBotsNeverMix(t *testing.T) {
	for _, demo := range []bool{false, true} {
		store, x := simplifierTown(t, demo)
		ran := false
		bot := botFunc(func(context.Context, botapi.Request, func(botapi.Event)) (botapi.Result, error) {
			ran = true
			return botapi.Result{}, nil
		})
		workers := &BotWorkers{Root: t.TempDir(), Store: store, Bots: map[Role]botapi.Bot{Simplifier: remoteBot(t, bot, Simplifier, "v1.0.0", !demo)}}
		_, err := workers.Run(context.Background(), x, Simplifier, func(Progress) {}, quiet())
		if err == nil || ran || !strings.Contains(err.Error(), "demo") {
			t.Fatalf("demo service %v: err %v, ran %v", demo, err, ran)
		}
		if store.Snapshot().Towns[x.ID].Workers[Simplifier].Run != nil {
			t.Fatal("a refused dispatch was recorded as sent")
		}
	}
}

// An unreachable bot was never sent the work, so the failure is ordinary; a
// stream that breaks after the bot took the work leaves the outcome
// uncertain, which Town reconciles before sending it again.
func TestTransportFailuresAreClassified(t *testing.T) {
	store, x := simplifierTown(t, false)
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	client, _ := botclient.New(down.URL, remoteToken, BotName(Simplifier), "simplifier", "")
	workers := &BotWorkers{Root: t.TempDir(), Store: store, Bots: map[Role]botapi.Bot{Simplifier: client}}
	_, err := workers.Run(context.Background(), x, Simplifier, func(Progress) {}, quiet())
	var interrupted *WorkerInterruptedError
	if err == nil || errors.As(err, &interrupted) || !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("unreachable bot: %v", err)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/identity" {
			_, _ = io.WriteString(w, `{"name":"simplifier-bot","role":"simplifier","version":"v1.0.0","contract":1}`)
			return
		}
		w.Header().Set("Content-Type", botapi.StreamType)
		_, _ = io.WriteString(w, `{"seq":0,"hello":{"name":"simplifier-bot","role":"simplifier","version":"v1.0.0","contract":1}}`+"\n")
	}))
	defer broken.Close()
	client, _ = botclient.New(broken.URL, remoteToken, BotName(Simplifier), "simplifier", "")
	workers.Bots[Simplifier] = client
	_, err = workers.Run(context.Background(), x, Simplifier, func(Progress) {}, quiet())
	if !errors.As(err, &interrupted) || !errors.Is(err, botapi.ErrUncertain) {
		t.Fatalf("broken stream: %v", err)
	}
	if run := store.Snapshot().Towns[x.ID].Workers[Simplifier].Run; run == nil || run.Version != "v1.0.0" {
		t.Fatalf("the uncertain run's record was lost: %+v", run)
	}
}

// Bots reach a managed dispatch's agent on Town's callback listener, and only
// with the token minted for that dispatch.
func TestAgentCallbackRoutesOnlyAuthorizedRequests(t *testing.T) {
	callback, err := NewAgentCallback("http://bt:8091/")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(callback.Handler())
	defer ts.Close()
	b := &BotWorkers{Callback: callback}
	head := strings.Repeat("a", 40)
	endpoint, token, close, err := b.startRemoteAgent(t.Context(), &managedDispatch{failed: true}, head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(endpoint, "http://bt:8091/v1/agent/") || len(token) != 64 {
		t.Fatalf("endpoint %q", endpoint)
	}
	path := strings.TrimPrefix(endpoint, "http://bt:8091")
	post := func(path, token string) int {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(`{"protocol":1,"head":"`+head+`","prompt":"review"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := post(path, "wrong"); got != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", got)
	}
	if got := post("/v1/agent/unknown", token); got != http.StatusNotFound {
		t.Fatalf("unknown route: %d", got)
	}
	// The dispatch refuses (failed), but only after the token was accepted.
	if got := post(path, token); got != http.StatusConflict {
		t.Fatalf("authorized request: %d", got)
	}
	close()
	if got := post(path, token); got != http.StatusNotFound {
		t.Fatalf("closed route: %d", got)
	}
	for _, bad := range []string{"ftp://x", "http://", "http://u:p@x", "http://x?q=1", "http://gateway:8091/town"} {
		if _, err := NewAgentCallback(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// The demo loop dispatches to each demo bot over the network and records the
// answer in Activity.
func TestDemoBotsRecordEachAnswer(t *testing.T) {
	store := testStore(t, true)
	if err := store.Update(seedDemo); err != nil {
		t.Fatal(err)
	}
	bots := map[Role]botapi.Bot{}
	for _, role := range AgentRoles {
		bots[role] = demoServed(t, role)
	}
	workers := &BotWorkers{Root: t.TempDir(), Store: store, Bots: bots}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runDemoBots(ctx, store, workers, time.Millisecond, 20*time.Millisecond) }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		answered := 0
		for _, e := range store.Snapshot().Events {
			if e.Kind == "error" {
				t.Fatalf("demo dispatch failed: %s", e.Title)
			}
			if strings.Contains(e.Title, "answered a demo dispatch") {
				answered++
			}
		}
		if answered == len(AgentRoles) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d bots answered", answered)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Further rounds answer the same way, which Activity does not repeat.
	time.Sleep(500 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	answered := 0
	for _, e := range store.Snapshot().Events {
		if strings.Contains(e.Title, "answered a demo dispatch") {
			answered++
		}
	}
	if answered != len(AgentRoles) {
		t.Fatalf("Activity holds %d answers for %d bots", answered, len(AgentRoles))
	}
}

// demoServed runs the real demo bot behind the network contract.
func demoServed(t *testing.T, role Role) botapi.Bot {
	t.Helper()
	t.Setenv("BROKK_BOT_TOKEN", remoteToken)
	dir := t.TempDir()
	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stdout := &lineWriter{lines: ready}
	go func() {
		done <- botserver.Run(ctx, []string{"--listen", "127.0.0.1:0", "--root", dir, "--demo"}, "v9.9.9", func(root string) botapi.Worker {
			return botapi.Worker{Name: BotName(role), Role: string(role), Root: root}
		}, strings.NewReader(""), stdout, io.Discard)
	}()
	t.Cleanup(func() { cancel(); <-done })
	var line string
	select {
	case line = <-ready:
	case err := <-done:
		t.Fatal(err)
	}
	address := line[strings.Index(line, `"address":"`)+len(`"address":"`):]
	address = address[:strings.Index(address, `"`)]
	client, err := botclient.New("http://"+address, remoteToken, BotName(role), string(role), "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type lineWriter struct{ lines chan string }

func (w *lineWriter) Write(p []byte) (int, error) {
	select {
	case w.lines <- string(p):
	default:
	}
	return len(p), nil
}

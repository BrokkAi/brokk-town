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

func simplifierTown(t *testing.T) (*Store, *Town) {
	store := testStore(t)
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
	store, x := simplifierTown(t)
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

// A bot from a release that had a demo mode may still run in it and answer
// with canned results. Town never sends it real work.
func TestADemoBotIsNeverSentWork(t *testing.T) {
	store, x := simplifierTown(t)
	ran := false
	bot := botFunc(func(context.Context, botapi.Request, func(botapi.Event)) (botapi.Result, error) {
		ran = true
		return botapi.Result{}, nil
	})
	workers := &BotWorkers{Root: t.TempDir(), Store: store, Bots: map[Role]botapi.Bot{Simplifier: remoteBot(t, bot, Simplifier, "v1.0.0", true)}}
	_, err := workers.Run(context.Background(), x, Simplifier, func(Progress) {}, quiet())
	if err == nil || ran || !strings.Contains(err.Error(), "demo mode") {
		t.Fatalf("err %v, ran %v", err, ran)
	}
	if store.Snapshot().Towns[x.ID].Workers[Simplifier].Run != nil {
		t.Fatal("a refused dispatch was recorded as sent")
	}
}

// An unreachable bot was never sent the work, so the failure is ordinary; a
// stream that breaks after the bot took the work leaves the outcome
// uncertain, which Town reconciles before sending it again.
func TestTransportFailuresAreClassified(t *testing.T) {
	store, x := simplifierTown(t)
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

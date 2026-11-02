package botclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

const token = "0123456789abcdef0123456789abcdef"

// fakeBot is a bot that runs whatever its test tells it to.
type fakeBot struct {
	run     func(context.Context, botapi.Request, func(botapi.Event)) (botapi.Result, error)
	storage func(context.Context, botapi.StorageRequest) (botapi.StorageResult, error)
}

func (f *fakeBot) Run(ctx context.Context, r botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
	return f.run(ctx, r, emit)
}

func (f *fakeBot) Storage(ctx context.Context, r botapi.StorageRequest) (botapi.StorageResult, error) {
	return f.storage(ctx, r)
}

func serve(t *testing.T, bot botapi.Bot) (*Client, *httptest.Server) {
	t.Helper()
	server := &botserver.Server{Bot: bot, Identity: botapi.Identity{Name: "review-bot", Role: "review", Version: "v1.2.3", Contract: botapi.Version}, Token: token, Heartbeat: 20 * time.Millisecond}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	client, err := New(ts.URL, token, "review-bot", "review", "")
	if err != nil {
		t.Fatal(err)
	}
	return client, ts
}

func request() botapi.Request {
	return botapi.Request{Town: "0123abcd", Role: "review", Repo: "o/r", PR: 7, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
}

func TestRunRelaysEventsInOrderAndTheResult(t *testing.T) {
	var got botapi.Request
	client, _ := serve(t, &fakeBot{run: func(_ context.Context, r botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
		got = r
		for i := range 50 {
			emit(botapi.Event{Progress: &botapi.Progress{Phase: "working", Task: strings.Repeat("x", i)}})
		}
		emit(botapi.Event{Log: &botapi.Log{Level: "INFO", Message: "done", Attrs: map[string]string{"k": "v"}}})
		time.Sleep(60 * time.Millisecond) // long enough for heartbeats between frames
		return botapi.Result{Review: &botapi.ReviewResult{Status: "submitted", Complete: true, ExactHead: r.HeadSHA}}, nil
	}})
	var events []botapi.Event
	result, err := client.Run(context.Background(), request(), func(e botapi.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if got.PR != 7 || got.Town != "0123abcd" {
		t.Fatalf("bot received %+v", got)
	}
	if len(events) != 51 || events[49].Progress == nil || len(events[49].Progress.Task) != 49 || events[50].Log == nil || events[50].Log.Attrs["k"] != "v" {
		t.Fatalf("events = %d %+v", len(events), events[len(events)-1])
	}
	if result.Review == nil || !result.Review.Complete || result.Review.ExactHead != strings.Repeat("b", 40) {
		t.Fatalf("result = %+v", result)
	}
	identity, err := client.Identity(context.Background())
	if err != nil || identity.Version != "v1.2.3" {
		t.Fatalf("identity = %+v, %v", identity, err)
	}
}

func TestABotFailureIsReportedWithItsPartialResult(t *testing.T) {
	client, _ := serve(t, &fakeBot{run: func(context.Context, botapi.Request, func(botapi.Event)) (botapi.Result, error) {
		return botapi.Result{Transcripts: []botapi.Transcript{{Path: "state/sessions/a.jsonl"}}}, errors.New("verification failed")
	}})
	result, err := client.Run(context.Background(), request(), nil)
	var botErr *botapi.BotError
	if !errors.As(err, &botErr) || botErr.Message != "verification failed" || errors.Is(err, botapi.ErrUncertain) {
		t.Fatalf("err = %v", err)
	}
	if len(result.Transcripts) != 1 {
		t.Fatalf("partial result lost: %+v", result)
	}
}

func TestRefusedRequestsAreUnavailableNotUncertain(t *testing.T) {
	ran := false
	client, ts := serve(t, &fakeBot{run: func(context.Context, botapi.Request, func(botapi.Event)) (botapi.Result, error) {
		ran = true
		return botapi.Result{}, nil
	}})
	wrong := *client
	wrong.Token = "not-the-token-at-all"
	if _, err := wrong.Run(context.Background(), request(), nil); !errors.Is(err, botapi.ErrUnavailable) || errors.Is(err, botapi.ErrUncertain) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := wrong.Identity(context.Background()); !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("wrong token identity: %v", err)
	}
	bad := request()
	bad.Town = "../escape"
	if _, err := client.Run(context.Background(), bad, nil); !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("bad workspace: %v", err)
	}
	// Unknown fields are refused rather than ignored.
	req, _ := http.NewRequest("POST", ts.URL+"/v1/run", strings.NewReader(`{"town":"0123abcd","role":"review","surprise":true}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(botapi.RunHeader, "0123456789abcdef")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", resp.StatusCode)
	}
	// An oversized body is refused before it is read in full.
	req, _ = http.NewRequest("POST", ts.URL+"/v1/run", strings.NewReader(`{"town":"`+strings.Repeat("a", botapi.MaxRequestBytes)+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(botapi.RunHeader, "0123456789abcdef")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized status = %d", resp.StatusCode)
	}
	ts.Close()
	if _, err := client.Run(context.Background(), request(), nil); !errors.Is(err, botapi.ErrUnavailable) || errors.Is(err, botapi.ErrUncertain) {
		t.Fatalf("closed server: %v", err)
	}
	if ran {
		t.Fatal("a refused request reached the bot")
	}
	// healthz needs no credentials.
	if _, err := New("ftp://x", token, "review-bot", "review", ""); err == nil {
		t.Fatal("accepted a non-http URL")
	}
	if _, err := New("http://x", "", "review-bot", "review", ""); err == nil {
		t.Fatal("accepted a URL without a token")
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	_, ts := serve(t, &fakeBot{})
	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}
	resp, err = http.Get(ts.URL + "/v1/identity")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("identity without token = %d", resp.StatusCode)
	}
}

// Canceling the service's context stops the bot, and the bot still reports
// what it completed before the client returns.
func TestCancellationStopsTheRunAndKeepsItsReport(t *testing.T) {
	started := make(chan struct{})
	client, _ := serve(t, &fakeBot{run: func(ctx context.Context, _ botapi.Request, _ func(botapi.Event)) (botapi.Result, error) {
		close(started)
		<-ctx.Done()
		return botapi.Result{Transcripts: []botapi.Transcript{{Path: "state/sessions/stopped.jsonl"}}}, context.Cause(ctx)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	result, err := client.Run(ctx, request(), nil)
	if err == nil || len(result.Transcripts) != 1 {
		t.Fatalf("canceled run = %+v, %v", result, err)
	}
}

// A service that goes away cancels the bot's run.
func TestDisconnectCancelsTheRun(t *testing.T) {
	stopped := make(chan error, 1)
	started := make(chan struct{})
	client, _ := serve(t, &fakeBot{run: func(ctx context.Context, _ botapi.Request, _ func(botapi.Event)) (botapi.Result, error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return botapi.Result{}, ctx.Err()
	}})
	client.Grace = time.Millisecond
	// Lose the cancel request, so only the closed stream can stop the run.
	client.HTTP = &http.Client{Transport: dropCancel{http.DefaultTransport}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	if _, err := client.Run(ctx, request(), nil); !errors.Is(err, botapi.ErrUncertain) {
		t.Fatalf("abandoned run: %v", err)
	}
	select {
	case err := <-stopped:
		if err == nil {
			t.Fatal("run not canceled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the bot kept running after the service left")
	}
}

type dropCancel struct{ next http.RoundTripper }

func (d dropCancel) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/v1/cancel" {
		return nil, errors.New("lost")
	}
	return d.next.RoundTrip(r)
}

// Any stream that breaks the contract leaves the outcome uncertain.
func TestBrokenStreamsAreUncertain(t *testing.T) {
	hello := `{"seq":0,"hello":{"name":"review-bot","role":"review","version":"v1","contract":1}}` + "\n"
	cases := map[string]string{
		"no terminal":        hello + `{"seq":1,"heartbeat":true}` + "\n",
		"out of order":       hello + `{"seq":2,"heartbeat":true}` + "\n",
		"no hello":           `{"seq":0,"heartbeat":true}` + "\n",
		"wrong bot":          `{"seq":0,"hello":{"name":"bug-bot","role":"bug","version":"v1","contract":1}}` + "\n",
		"wrong contract":     `{"seq":0,"hello":{"name":"review-bot","role":"review","version":"v1","contract":99}}` + "\n",
		"two kinds":          hello + `{"seq":1,"heartbeat":true,"done":{"result":{}}}` + "\n",
		"empty event":        hello + `{"seq":1,"event":{}}` + "\n",
		"garbage":            hello + "not json\n",
		"trailing frames":    hello + `{"seq":1,"done":{"result":{}}}` + "\n" + `{"seq":2,"heartbeat":true}` + "\n",
		"oversized event":    hello + `{"seq":1,"event":{"log":{"message":"` + strings.Repeat("x", botapi.MaxEventBytes) + `"}}}` + "\n",
		"truncated line":     hello + `{"seq":1,"done":{"res`,
		"not a frame stream": "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "not a frame stream" {
					w.Header().Set("Content-Type", "text/html")
				} else {
					w.Header().Set("Content-Type", botapi.StreamType)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer ts.Close()
			client, _ := New(ts.URL, token, "review-bot", "review", "")
			if _, err := client.Run(context.Background(), request(), nil); !errors.Is(err, botapi.ErrUncertain) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestASilentBotIsUncertain(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", botapi.StreamType)
		_, _ = io.WriteString(w, `{"seq":0,"hello":{"name":"review-bot","role":"review","version":"v1","contract":1}}`+"\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	defer close(release)
	client, _ := New(ts.URL, token, "review-bot", "review", "")
	client.Idle = 100 * time.Millisecond
	_, err := client.Run(context.Background(), request(), nil)
	if !errors.Is(err, botapi.ErrUncertain) || !strings.Contains(err.Error(), "silent") {
		t.Fatalf("err = %v", err)
	}
}

func TestStorageRoundTripsAndFailuresAreClassified(t *testing.T) {
	var mu sync.Mutex
	var got botapi.StorageRequest
	client, ts := serve(t, &fakeBot{storage: func(_ context.Context, r botapi.StorageRequest) (botapi.StorageResult, error) {
		mu.Lock()
		got = r
		mu.Unlock()
		return botapi.StorageResult{Entries: []botapi.StorageEntry{{Path: "checkout", Kind: "workspace", Bytes: 3}}, Removed: []botapi.StorageRemoval{{Path: "state/sessions/a.jsonl", Status: "removed"}}}, nil
	}})
	result, err := client.Storage(context.Background(), botapi.StorageRequest{Town: "0123abcd", Role: "review", Verify: []string{"state/sessions/a.jsonl"}})
	if err != nil || len(result.Entries) != 1 || result.Removed[0].Status != "removed" || got.Verify[0] != "state/sessions/a.jsonl" {
		t.Fatalf("storage = %+v, %v (sent %+v)", result, err, got)
	}
	if _, err := client.Storage(context.Background(), botapi.StorageRequest{Town: "0123abcd", Role: "bug"}); !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("wrong role: %v", err)
	}
	ts.Close()
	if _, err := client.Storage(context.Background(), botapi.StorageRequest{Town: "0123abcd", Role: "review"}); !errors.Is(err, botapi.ErrUnavailable) {
		t.Fatalf("closed: %v", err)
	}
}

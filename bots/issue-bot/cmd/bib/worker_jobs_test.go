package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	bot "github.com/BrokkAi/issue-bot"
	"github.com/BrokkAi/issue-bot/internal/worker"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerJobSummaryAndTargetedRetryPreserveSavedEvidence(t *testing.T) {
	root, err := os.MkdirTemp("", "issue-protocol-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	cfg := bot.DefaultConfig()
	cfg.Remote = "https://github.com/acme/fixture.git"
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	cfg.Branch = "main"
	cfg.GitHub.Repo = "acme/fixture"
	cfg.GitHub.Host = "github.com"
	if err = os.MkdirAll(cfg.StateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(cfg.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	saved := bot.State{Format: 1, Remote: cfg.Remote, Branch: cfg.Branch, Directory: cfg.Directory, Repo: cfg.GitHub.Repo, Host: cfg.GitHub.Host, Jobs: map[int]*bot.Job{
		7: {Issue: bot.Issue{Number: 7}, Status: "blocked", Tries: 3, RetryAt: time.Now().Add(time.Hour), ClaimPending: true, Failure: "uncertain publication", Result: &bot.Result{Status: "blocked", Detail: "saved evidence"}},
		8: {Issue: bot.Issue{Number: 8}, Status: "blocked", Tries: 2},
	}}
	data, _ := json.Marshal(saved)
	if err = os.WriteFile(filepath.Join(cfg.StateDirectory, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socket := filepath.Join(root, "worker.sock")
	done := make(chan error, 1)
	go func() { done <- workerCommand(ctx, []string{"--socket", socket}, "v1.0.0") }()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer client.CloseIdleConnections()
	ready := false
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
		r, e := client.Get("http://worker/v1/initialize")
		if e == nil {
			r.Body.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("worker not ready")
	}
	query := func(mode string, issue int) map[int]*worker.JobSummary {
		t.Helper()
		request := worker.Request{Protocol: 1, Remote: cfg.Remote, Branch: cfg.Branch, Directory: cfg.Directory, StateDirectory: cfg.StateDirectory, Repo: cfg.GitHub.Repo, Host: cfg.GitHub.Host, Mode: mode, Issue: issue}
		body, _ := json.Marshal(request)
		response, e := client.Post("http://worker/v1/runs", "application/json", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		var jobs map[int]*worker.JobSummary
		complete := false
		for scanner.Scan() {
			var event worker.Event
			if e = json.Unmarshal(scanner.Bytes(), &event); e != nil {
				t.Fatal(e)
			}
			if event.Type == "error" {
				t.Fatal(event.Error)
			}
			if event.Result != nil {
				jobs = event.Result.Jobs
			}
			if event.Type == "complete" {
				complete = true
			}
		}
		if scanner.Err() != nil || !complete {
			t.Fatal("incomplete response", scanner.Err())
		}
		return jobs
	}
	jobs := query("jobs", 0)
	if jobs[7].Status != "blocked" || !jobs[7].ClaimPending || jobs[7].Result.Detail != "saved evidence" {
		t.Fatalf("summary lost evidence: %+v", jobs[7])
	}
	jobs = query("retry-issue", 7)
	if jobs[7].Status != "pending" || jobs[7].Tries != 0 || !jobs[7].RetryAt.IsZero() || jobs[8].Tries != 2 {
		t.Fatalf("incorrect retry: %+v", jobs)
	}
	after, err := bot.ReadState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Jobs[7].ClaimPending || after.Jobs[7].Result.Detail != "saved evidence" {
		t.Fatal("retry lost durable evidence")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

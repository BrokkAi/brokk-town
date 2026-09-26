package reviewbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// The remote session already owns the exact checkout. Reconstruct the complete
// diff there rather than spending the transport's prompt budget on a second copy.
// Discussion and review metadata remain complete; never truncate review input.
func remoteSnapshotContext(snapshot Snapshot) (string, error) {
	if !validCommit(snapshot.MergeBase) || !validCommit(snapshot.PR.Head.SHA) {
		return "", errors.New("remote snapshot requires exact merge-base and head commits")
	}
	snapshot.Diff = fmt.Sprintf("Read the complete diff from the exact checkout: git diff --no-ext-diff --no-textconv --no-color --find-renames %s %s -- .\nRun this command from the repository root. If either commit is unavailable, stop and report the limitation; never substitute another revision.", snapshot.MergeBase, snapshot.PR.Head.SHA)
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return "in the following inline data (paths are repository-relative; the diff field gives the command for the complete diff):\n" + string(data) + "\nEnd of snapshot data. Remove temporary reproductions before finishing.", nil
}

// The parent owns target execution and artifact validation. Review Bot retains
// investigation, independent finding verification and exact-revision publication.
func executeRemote(ctx context.Context, c Config, prompt string) (string, error) {
	if !utf8.ValidString(prompt) || strings.TrimSpace(prompt) == "" || utf8.RuneCountInString(prompt) > 65536 {
		return "", errors.New("remote review prompt exceeds the 65536-character limit or is empty; no session was requested")
	}
	if !validCommit(c.RemoteHead) {
		return "", errors.New("remote review requires a bounded prompt and exact head")
	}
	body, _ := json.Marshal(struct {
		Protocol int    `json:"protocol"`
		Head     string `json:"head"`
		Prompt   string `json:"prompt"`
	}{1, c.RemoteHead, prompt})
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.RemoteAgent)
	}, MaxResponseHeaderBytes: 8 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://town/v1/agent", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return "", errors.Join(errors.New("remote review outcome was not confirmed; inspect the parent's retained run before retrying"), ctx.Err())
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", errors.New("parent refused remote review evidence; inspect its retained run")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return "", errors.New("remote review answer was incomplete")
	}
	var answer struct {
		Protocol int    `json:"protocol"`
		Head     string `json:"head"`
		Text     string `json:"text"`
		Evidence string `json:"evidence"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&answer) != nil || decoder.Decode(new(any)) != io.EOF || answer.Protocol != 1 || answer.Head != c.RemoteHead || answer.Evidence == "" || len(answer.Text) > 1<<20 || strings.TrimSpace(answer.Text) == "" {
		return "", errors.New("remote review answer lacks matching revision and evidence")
	}
	return answer.Text, nil
}

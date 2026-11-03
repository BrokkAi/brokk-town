// Package botclient reaches a bot over the network contract described in
// package botapi. A Client is a botapi.Bot, so the service dispatches to a bot
// in another process, container or machine exactly as it would to one in its
// own process.
package botclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// Client is one bot reached over HTTP.
type Client struct {
	// URL is the bot's base address, such as http://review-bot:8080.
	URL string
	// Token is the bearer credential the bot expects.
	Token string
	// Name and Role are what the bot must report about itself.
	Name, Role string
	// HTTP carries the requests. Nil uses a transport that never follows a
	// proxy from the environment.
	HTTP *http.Client
	// Idle is how long a run stream may stay silent before the bot is taken
	// for gone. Zero means one minute; the bot sends a heartbeat every ten
	// seconds.
	Idle time.Duration
	// Grace is how long a canceled run may take to report what it completed
	// before the stream is closed. Zero means 30 seconds.
	Grace time.Duration
}

// New returns a client for a bot at base, checking the address. caFile, when
// set, names PEM certificates to trust for an https address.
func New(base, token, name, role, caFile string) (*Client, error) {
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s: the bot URL must be http(s)://host[:port] with no credentials, query or fragment", name)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("%s: a bot URL needs a bearer token", name)
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:    10 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		IdleConnTimeout:        90 * time.Second,
		ForceAttemptHTTP2:      false,
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("%s: read the CA file: %w", name, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s: the CA file holds no PEM certificate", name)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Client{
		URL: strings.TrimRight(parsed.String(), "/"), Token: strings.TrimSpace(token), Name: name, Role: role,
		HTTP: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *Client) unavailable(format string, args ...any) error {
	return fmt.Errorf("%s: %w: %s", c.Name, botapi.ErrUnavailable, fmt.Sprintf(format, args...))
}

func (c *Client) uncertain(format string, args ...any) error {
	return fmt.Errorf("%s: %w: %s", c.Name, botapi.ErrUncertain, fmt.Sprintf(format, args...))
}

// send performs one request and reports whether it ever reached the wire, so
// a failure can be told apart from one the bot may have acted on.
func (c *Client) send(ctx context.Context, method, path string, body []byte, header http.Header) (*http.Response, bool, error) {
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(body)
	}
	var wrote atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) }})
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, input)
	if err != nil {
		return nil, false, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	// A request with a body may change the bot's state, so it never rides a
	// pooled connection: a write into a connection the bot already closed
	// would look as if the bot might have acted. On a fresh connection a bot
	// that is gone fails at dial, before anything is sent.
	req.Close = body != nil
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	return resp, wrote.Load(), err
}

// refusal describes a non-success answer without echoing more than a short
// message the bot chose to send.
func refusal(resp *http.Response) string {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&body)
	if body.Error == "" {
		return resp.Status
	}
	return resp.Status + ": " + botapi.Truncate(body.Error, 1024)
}

// Identity asks the bot who it is and checks it is the bot this client is for.
func (c *Client) Identity(ctx context.Context) (botapi.Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, _, err := c.send(ctx, http.MethodGet, "/v1/identity", nil, nil)
	if err != nil {
		return botapi.Identity{}, c.unavailable("%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return botapi.Identity{}, c.unavailable("identity refused: %s", refusal(resp))
	}
	var identity botapi.Identity
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&identity); err != nil {
		return botapi.Identity{}, c.unavailable("unreadable identity: %v", err)
	}
	if err := c.check(identity); err != nil {
		return botapi.Identity{}, err
	}
	return identity, nil
}

func (c *Client) check(identity botapi.Identity) error {
	if identity.Role != c.Role || (c.Name != "" && identity.Name != c.Name) {
		return c.unavailable("the endpoint is %s serving the %q role, not %s", botapi.Truncate(identity.Name, 64), botapi.Truncate(identity.Role, 64), c.Role)
	}
	if identity.Contract != botapi.Version {
		return c.unavailable("the bot speaks contract %d; this service speaks %d", identity.Contract, botapi.Version)
	}
	if identity.Version == "" || len(identity.Version) > 128 {
		return c.unavailable("the bot reported no usable version")
	}
	return nil
}

// Storage asks the bot for a workspace's inventory, or to remove transcripts.
func (c *Client) Storage(ctx context.Context, request botapi.StorageRequest) (botapi.StorageResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return botapi.StorageResult{}, err
	}
	if len(body) > botapi.MaxRequestBytes {
		return botapi.StorageResult{}, c.unavailable("the storage request exceeds the contract's bound")
	}
	resp, wrote, err := c.send(ctx, http.MethodPost, "/v1/storage", body, nil)
	if err != nil {
		if wrote {
			return botapi.StorageResult{}, c.uncertain("%v", err)
		}
		return botapi.StorageResult{}, c.unavailable("%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return botapi.StorageResult{}, c.unavailable("storage refused: %s", refusal(resp))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, botapi.MaxStorageBytes+1))
	if err != nil {
		return botapi.StorageResult{}, c.uncertain("storage answer broke off: %v", err)
	}
	if len(data) > botapi.MaxStorageBytes {
		return botapi.StorageResult{}, c.uncertain("storage answer exceeds the contract's bound")
	}
	var result botapi.StorageResult
	if err := json.Unmarshal(data, &result); err != nil {
		return botapi.StorageResult{}, c.uncertain("unreadable storage answer: %v", err)
	}
	return result, nil
}

// Run sends one operation and relays the bot's events while it runs. When ctx
// ends the client asks the bot to stop and waits a short grace for the result
// it completed; a stream that breaks, goes silent or breaks the contract
// leaves the outcome uncertain.
func (c *Client) Run(ctx context.Context, request botapi.Request, emit func(botapi.Event)) (botapi.Result, error) {
	if request.Role != c.Role {
		return botapi.Result{}, fmt.Errorf("%s cannot run the %q role", c.Name, request.Role)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return botapi.Result{}, err
	}
	if len(body) > botapi.MaxRequestBytes {
		return botapi.Result{}, c.unavailable("the run request exceeds the contract's bound")
	}
	if err := ctx.Err(); err != nil {
		return botapi.Result{}, c.unavailable("the run was canceled before it was sent: %v", err)
	}
	id, err := botapi.NewToken()
	if err != nil {
		return botapi.Result{}, err
	}
	// The stream outlives ctx by the grace period, so a canceled run can
	// still report what it completed.
	stream, closeStream := context.WithCancel(context.WithoutCancel(ctx))
	defer closeStream()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-finished:
			return
		case <-ctx.Done():
		}
		// The bot registers a run only once it has read the request, so a
		// cancel that arrives first is tried again.
		for attempt := 0; attempt < 5 && !c.cancelRun(id); attempt++ {
			select {
			case <-finished:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		grace := c.Grace
		if grace <= 0 {
			grace = 30 * time.Second
		}
		select {
		case <-finished:
		case <-time.After(grace):
			closeStream()
		}
	}()
	idle := c.Idle
	if idle <= 0 {
		idle = time.Minute
	}
	// The watchdog also covers the wait for the response's headers, which the
	// bot writes as soon as it has read the request.
	watchdog := time.AfterFunc(idle, closeStream)
	defer watchdog.Stop()
	resp, wrote, err := c.send(stream, http.MethodPost, "/v1/run", body, http.Header{botapi.RunHeader: {id}})
	if err != nil {
		if !wrote && ctx.Err() == nil {
			return botapi.Result{}, c.unavailable("%v", err)
		}
		return botapi.Result{}, errors.Join(c.uncertain("%v", err), ctx.Err())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message := refusal(resp)
		// A gateway between the service and the bot may have forwarded the
		// request before it failed; the bot itself refuses before acting.
		if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout {
			return botapi.Result{}, c.uncertain("run refused: %s", message)
		}
		return botapi.Result{}, c.unavailable("run refused: %s", message)
	}
	if media := resp.Header.Get("Content-Type"); !strings.HasPrefix(media, botapi.StreamType) {
		return botapi.Result{}, c.uncertain("the run answered %q, not a frame stream", botapi.Truncate(media, 64))
	}
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	for seq := 0; ; seq++ {
		line, err := readLine(reader, botapi.MaxResultBytes)
		if err != nil {
			reason := err.Error()
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				reason = "the stream ended before the run's result"
			} else if stream.Err() != nil && ctx.Err() == nil {
				reason = "the bot went silent for " + idle.String()
			}
			return botapi.Result{}, errors.Join(c.uncertain("%s", reason), ctx.Err())
		}
		watchdog.Reset(idle)
		var frame botapi.Frame
		if err := json.Unmarshal(line, &frame); err != nil {
			return botapi.Result{}, c.uncertain("unreadable frame %d: %v", seq, err)
		}
		if frame.Seq != seq {
			return botapi.Result{}, c.uncertain("frame %d arrived as %d", seq, frame.Seq)
		}
		set := 0
		for _, present := range []bool{frame.Hello != nil, frame.Event != nil, frame.Heartbeat, frame.Done != nil} {
			if present {
				set++
			}
		}
		if set != 1 {
			return botapi.Result{}, c.uncertain("frame %d carries %d kinds of content", seq, set)
		}
		if frame.Done == nil && len(line) > botapi.MaxEventBytes {
			return botapi.Result{}, c.uncertain("frame %d exceeds the event bound", seq)
		}
		switch {
		case seq == 0:
			if frame.Hello == nil {
				return botapi.Result{}, c.uncertain("the stream did not open with the bot's identity")
			}
			if err := c.check(*frame.Hello); err != nil {
				return botapi.Result{}, errors.Join(err, botapi.ErrUncertain)
			}
		case frame.Hello != nil:
			return botapi.Result{}, c.uncertain("frame %d repeats the bot's identity", seq)
		case frame.Event != nil:
			e := *frame.Event
			if (e.Progress == nil) == (e.Log == nil) {
				return botapi.Result{}, c.uncertain("event %d must carry exactly one of progress or log", seq)
			}
			if emit != nil && ctx.Err() == nil {
				emit(e)
			}
		case frame.Done != nil:
			if rest, _ := reader.Peek(1); len(rest) > 0 {
				if trailing, _ := io.ReadAll(io.LimitReader(reader, 4096)); len(bytes.TrimSpace(trailing)) > 0 {
					return botapi.Result{}, c.uncertain("the stream continued after the run's result")
				}
			}
			var err error
			if frame.Done.Error != "" {
				err = &botapi.BotError{Message: botapi.Truncate(frame.Done.Error, 64<<10)}
			}
			if frame.Done.Uncertain {
				err = errors.Join(c.uncertain("the bot stopped the run before it finished"), err)
			}
			return frame.Done.Result, err
		}
	}
}

// cancelRun asks the bot to stop a run and reports whether the bot took it.
// It is best effort: if every request is lost, closing the stream after the
// grace period stops the run as well.
func (c *Client) cancelRun(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, _, err := c.send(ctx, http.MethodPost, "/v1/cancel", nil, http.Header{botapi.RunHeader: {id}})
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

// readLine reads one newline-terminated line of at most limit bytes.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > limit+1 {
			return nil, fmt.Errorf("a frame exceeds the contract's %d-byte bound", limit)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return bytes.TrimRight(line, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}

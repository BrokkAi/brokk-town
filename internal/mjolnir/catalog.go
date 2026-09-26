// Package mjolnir reads the daemon's versioned, public launch catalog. It never
// starts the daemon, a session, an agent, or a repository operation.
package mjolnir

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const maxBytes = 1 << 20
const maxAge = time.Minute

type Profile struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
}
type Target struct {
	ID                       string `json:"id"`
	Kind                     string `json:"kind"`
	RequiresProjectDirectory bool   `json:"requires_project_directory"`
	Availability             string `json:"availability"`
	UnavailableReason        string `json:"unavailable_reason,omitempty"`
	Host                     string `json:"host,omitempty"`
}
type Repository struct {
	ID          string `json:"id"`
	GitHub      string `json:"github,omitempty"`
	Destination string `json:"destination"`
}
type Bundle struct {
	ID                string       `json:"id"`
	PrimaryRepository string       `json:"primary_repository"`
	Repositories      []Repository `json:"repositories"`
}
type Host struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Targets    []string `json:"targets"`
	Stale      bool     `json:"stale"`
	Refreshing bool     `json:"refreshing"`
	HasError   bool     `json:"has_error"`
}

// Selection contains references only. The empty pair means direct local
// execution, distinct from any daemon-owned target (including local targets).
type Selection struct {
	Target  string `json:"target_id"`
	Profile string `json:"profile_id"`
}

func (s Selection) Managed() bool { return s.Target != "" }
func (s Selection) Validate() error {
	if s == (Selection{}) || (ValidID(s.Target) && ValidID(s.Profile)) {
		return nil
	}
	return errors.New("execution requires both a Mjolnir target ID and profile ID, or an empty pair for direct local execution")
}
func ValidID(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

type Options struct {
	Revision uint64     `json:"revision"`
	Profiles []Profile  `json:"profiles"`
	Targets  []Target   `json:"targets"`
	Bundles  []Bundle   `json:"bundles"`
	Hosts    []Host     `json:"hosts,omitempty"`
	Default  *Selection `json:"default,omitempty"`
}
type Listing struct {
	Options
	Configured bool      `json:"configured"`
	Demo       bool      `json:"demo"`
	Fetched    time.Time `json:"fetched"`
	Stale      bool      `json:"stale"`
	Refreshing bool      `json:"refreshing"`
	Error      string    `json:"error,omitempty"`
}
type Connection struct{ URL, TokenFile string }

func Environment() Connection {
	return Connection{os.Getenv("BT_MJOLNIR_API_URL"), os.Getenv("BT_MJOLNIR_TOKEN_FILE")}
}

type Catalog struct {
	mu                          sync.RWMutex
	listing                     Listing
	connection                  Connection
	cache, identity, setupError string
	client                      *http.Client
	wake                        chan struct{}
	refresh                     sync.Mutex
}
type saved struct {
	Identity string    `json:"identity"`
	Fetched  time.Time `json:"fetched"`
	Options  Options   `json:"options"`
}

func New(dir string, demo bool, connection Connection) *Catalog {
	c := &Catalog{connection: connection, cache: filepath.Join(dir, "mjolnir-options.json"), wake: make(chan struct{}, 1)}
	c.listing = Listing{Demo: demo, Stale: true, Options: Options{Profiles: []Profile{}, Targets: []Target{}, Bundles: []Bundle{}}}
	// Demo ignores even an explicitly configured connection and the disk cache.
	if demo {
		return c
	}
	c.listing.Configured = connection.URL != "" || connection.TokenFile != ""
	if !c.listing.Configured {
		return c
	}
	u, err := url.Parse(connection.URL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimRight(u.Path, "/") != "/api/v1" || connection.TokenFile == "" {
		c.setupError = "Set BT_MJOLNIR_API_URL to the API base URL and BT_MJOLNIR_TOKEN_FILE to its token file (see mj api-info)."
	} else if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		c.setupError = "Town's Mjolnir API connection must use a loopback address."
	}
	c.listing.Error = c.setupError
	if c.setupError != "" {
		return c
	}
	hash := sha256.Sum256([]byte(strings.TrimRight(connection.URL, "/") + "\x00" + connection.TokenFile))
	c.identity = hex.EncodeToString(hash[:])
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	c.client = &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if data, err := readBounded(c.cache, maxBytes); err == nil {
		var record saved
		if json.Unmarshal(data, &record) == nil && record.Identity == c.identity && validate(record.Options) == nil {
			c.listing.Options, c.listing.Fetched = record.Options, record.Fetched
		}
	}
	return c
}

func copyListing(v Listing) Listing {
	b, _ := json.Marshal(v)
	var out Listing
	_ = json.Unmarshal(b, &out)
	return out
}

// List is memory-only; rendering and scheduling never wait for a daemon read.
func (c *Catalog) List() Listing {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := copyListing(c.listing)
	v.Stale = v.Fetched.IsZero() || time.Since(v.Fetched) > maxAge || v.Error != ""
	return v
}

// RequestRefresh coalesces requests. Run owns the bounded background I/O.
func (c *Catalog) RequestRefresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listing.Refreshing {
		return
	}
	if c.listing.Demo || !c.listing.Configured || c.setupError != "" {
		return
	}
	select {
	case c.wake <- struct{}{}:
		c.listing.Refreshing = true
	default:
	}
}
func (c *Catalog) Run(ctx context.Context) {
	if c.listing.Demo || !c.listing.Configured || c.setupError != "" {
		return
	}
	defer c.client.CloseIdleConnections()
	ticker := time.NewTicker(maxAge)
	defer ticker.Stop()
	if c.List().Stale {
		c.refreshNow(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			c.refreshNow(ctx)
		case <-ticker.C:
			c.refreshNow(ctx)
		}
	}
}

func (c *Catalog) refreshNow(ctx context.Context) {
	c.refresh.Lock()
	defer c.refresh.Unlock()
	if c.listing.Demo || !c.listing.Configured || c.setupError != "" || ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	c.listing.Refreshing = true
	c.mu.Unlock()
	options, err := c.fetch(ctx)
	if err == nil && ctx.Err() == nil {
		record := saved{Identity: c.identity, Fetched: time.Now().UTC(), Options: options}
		data, _ := json.Marshal(record)
		if e := writeCache(c.cache, data); e != nil {
			err = errors.New("Mjolnir options could not be cached; check Town's state directory permissions.")
		}
		c.mu.Lock()
		c.listing.Options, c.listing.Fetched = record.Options, record.Fetched
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listing.Refreshing = false
	if ctx.Err() != nil {
		return
	}
	c.listing.Error = ""
	if err != nil {
		c.listing.Error = err.Error()
	}
}
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("file too large")
	}
	return b, err
}
func writeCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mjolnir-options-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (c *Catalog) fetch(ctx context.Context) (Options, error) {
	var options Options
	if err := c.get(ctx, "/options", "options", &options); err != nil {
		return options, err
	}
	if validate(options) != nil {
		return Options{}, errors.New("Mjolnir returned invalid launch options; check its API version.")
	}
	return options, nil
}

// get reads only the public API. Error bodies may contain credentials, paths or
// commands, so callers receive status-specific instructions instead.
func (c *Catalog) get(ctx context.Context, path, resource string, out any) error {
	fail := errors.New
	if c.listing.Demo {
		return fail("Mjolnir is offline in demo mode.")
	}
	if c.setupError != "" {
		return fail(c.setupError)
	}
	if c.client == nil {
		return fail("Configure BT_MJOLNIR_API_URL and BT_MJOLNIR_TOKEN_FILE to read Mjolnir profiles.")
	}
	token, err := readBounded(c.connection.TokenFile, 4096)
	if err != nil || len(strings.TrimSpace(string(token))) == 0 {
		return fail("Cannot read Mjolnir's API token; check BT_MJOLNIR_TOKEN_FILE and its permissions.")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.connection.URL, "/")+path, nil)
	if err != nil {
		return fail("Invalid Mjolnir API URL; check BT_MJOLNIR_API_URL.")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := c.client.Do(req)
	if err != nil {
		return fail("Cannot reach Mjolnir; check that its daemon and API listener are running.")
	}
	defer resp.Body.Close()
	if resp.Header.Get("Mj-Api-Version") != "1" {
		return fail("Mjolnir returned an unsupported API version; use a daemon serving API 1.")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fail("Mjolnir rejected authentication; check its API token file.")
	}
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Sprintf("Mjolnir %s returned HTTP %d; check the selected profile, its runtime and authentication in Mjolnir.", resource, resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return fail("Mjolnir " + resource + " exceeded the response limit or could not be read.")
	}
	if json.Unmarshal(data, out) != nil {
		return fail("Mjolnir returned invalid " + resource + "; check its API version.")
	}
	return nil
}
func validate(o Options) error {
	if o.Profiles == nil || o.Targets == nil || o.Bundles == nil {
		return errors.New("missing catalog lists")
	}
	profiles, targets := map[string]bool{}, map[string]bool{}
	for _, p := range o.Profiles {
		if !ValidID(p.ID) || !ValidID(p.Harness) || profiles[p.ID] {
			return errors.New("invalid profile")
		}
		profiles[p.ID] = true
	}
	for _, t := range o.Targets {
		if !ValidID(t.ID) || !ValidID(t.Kind) || targets[t.ID] || len(t.UnavailableReason) > 4096 || strings.ContainsFunc(t.UnavailableReason, unicode.IsControl) {
			return errors.New("invalid target")
		}
		switch t.Availability {
		case "ready", "stale", "unavailable", "unknown":
		default:
			return errors.New("invalid availability")
		}
		targets[t.ID] = true
	}
	if o.Default != nil && (!profiles[o.Default.Profile] || !targets[o.Default.Target]) {
		return errors.New("invalid default")
	}
	return nil
}
func (c *Catalog) Contains(s Selection) bool {
	v := c.List()
	profile, target := false, false
	for _, p := range v.Profiles {
		profile = profile || p.ID == s.Profile
	}
	for _, t := range v.Targets {
		target = target || t.ID == s.Target
	}
	return profile && target
}

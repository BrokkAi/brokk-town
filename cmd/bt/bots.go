package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/botapi/botclient"
	"github.com/BrokkAi/brokk-town/internal/botlaunch"
	"github.com/BrokkAi/brokk-town/internal/town"
)

// fileBot is one bot's entry in the config file's bots object. Tokens are
// never written into the config file itself: token_file names a file, such
// as a mounted secret, that holds it.
type fileBot struct {
	URL       string `json:"url"`
	TokenFile string `json:"token_file"`
	CAFile    string `json:"ca_file"`
}

// decodeFileBots reads the config file's bots object, keyed by role key.
func decodeFileBots(raw json.RawMessage) (map[town.Role]fileBot, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var bots map[string]fileBot
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&bots); err != nil || bots == nil {
		return nil, errors.New("bots must be an object keyed by role, each with url and token_file")
	}
	out := map[town.Role]fileBot{}
	for key, bot := range bots {
		role := town.Role(key)
		if town.BotName(role) == "" {
			return nil, fmt.Errorf("bots: %q is not a role key", key)
		}
		if bot.URL == "" {
			return nil, fmt.Errorf("bots.%s: url is required", key)
		}
		out[role] = bot
	}
	return out, nil
}

// botEnv names one role's environment variable, such as
// BROKK_TOWN_BOT_REVIEW_URL.
func botEnv(role town.Role, suffix string) string {
	return "BROKK_TOWN_BOT_" + strings.ToUpper(string(role)) + "_" + suffix
}

// botFleet is the set of bots one service reaches, and the processes it
// started for the ones it runs itself.
type botFleet struct {
	bots     map[town.Role]botapi.Bot
	launched []*botlaunch.Process
	remote   map[town.Role]bool
}

// Stop stops every bot the service launched, in parallel.
func (f *botFleet) Stop() {
	var wg sync.WaitGroup
	for _, p := range f.launched {
		wg.Add(1)
		go func() { defer wg.Done(); p.Stop() }()
	}
	wg.Wait()
}

// townBots resolves each role's bot. A role with a URL, from its environment
// variable or the config file's bots object, is reached at that address with
// its token; that is how a docker compose or Kubernetes deployment runs. Any
// other role's binary is started on this machine on a loopback port, as the
// desktop app runs, keeping its workspaces under the service's own state
// directory.
func townBots(dir string, demo bool, file map[town.Role]fileBot, log *slog.Logger) (*botFleet, error) {
	fleet := &botFleet{bots: map[town.Role]botapi.Bot{}, remote: map[town.Role]bool{}}
	caFile := os.Getenv("BROKK_TOWN_BOT_CA_FILE")
	var local []town.Role
	for _, role := range town.AgentRoles {
		name := town.BotName(role)
		entry := file[role]
		if v := os.Getenv(botEnv(role, "URL")); v != "" {
			entry.URL = v
		}
		if v := os.Getenv(botEnv(role, "TOKEN_FILE")); v != "" {
			entry.TokenFile = v
		}
		if entry.URL == "" {
			local = append(local, role)
			continue
		}
		token := os.Getenv(botEnv(role, "TOKEN"))
		if entry.TokenFile != "" {
			data, err := os.ReadFile(entry.TokenFile)
			if err != nil {
				return nil, fmt.Errorf("%s: read the token file: %w", name, err)
			}
			token = strings.TrimSpace(string(data))
		}
		if token == "" {
			return nil, fmt.Errorf("%s has a URL but no token; set %s or %s", name, botEnv(role, "TOKEN"), botEnv(role, "TOKEN_FILE"))
		}
		ca := caFile
		if entry.CAFile != "" {
			ca = entry.CAFile
		}
		client, err := botclient.New(entry.URL, token, name, string(role), ca)
		if err != nil {
			return nil, err
		}
		fleet.bots[role] = client
		fleet.remote[role] = true
	}
	if len(local) == 0 {
		return fleet, nil
	}
	binDir, err := botDirectory()
	if err != nil {
		return nil, err
	}
	for _, role := range local {
		name := town.BotName(role)
		p := botlaunch.Start(botlaunch.Spec{Name: name, Role: string(role), Executable: filepath.Join(binDir, name+exeSuffix()), Root: dir, Demo: demo}, log)
		fleet.launched = append(fleet.launched, p)
		fleet.bots[role] = p
	}
	return fleet, nil
}

// botDirectory is where the bot binaries are installed: BROKK_TOWN_BOT_DIR,
// or the directory bt itself is in, as a release archive lays them out.
func botDirectory() (string, error) {
	if dir := os.Getenv("BROKK_TOWN_BOT_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	exe, err := executablePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// agentCallback starts the listener bots use to reach a managed dispatch's
// agent. BROKK_TOWN_CALLBACK_URL is the address bots use, and
// BROKK_TOWN_CALLBACK_LISTEN where the service listens for it (":8091" when
// only the URL is set). Without a URL the service listens on a loopback port,
// which bots it launched itself can reach. It returns nil when there is no
// listener to start.
func agentCallback(ctx context.Context, fleet *botFleet, log *slog.Logger) (*town.AgentCallback, func(), error) {
	advertised := os.Getenv("BROKK_TOWN_CALLBACK_URL")
	listen := os.Getenv("BROKK_TOWN_CALLBACK_LISTEN")
	if advertised == "" {
		if fleet.remote[town.Review] {
			log.Warn("review-bot is remote but BROKK_TOWN_CALLBACK_URL is not set; managed reviews will fail until it is")
			return nil, func() {}, nil
		}
		listen = "127.0.0.1:0"
	} else if listen == "" {
		listen = ":8091"
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, nil, fmt.Errorf("agent callback: %w", err)
	}
	if advertised == "" {
		advertised = "http://" + listener.Addr().String()
	}
	callback, err := town.NewAgentCallback(advertised)
	if err != nil {
		listener.Close()
		return nil, nil, err
	}
	server := &http.Server{Handler: callback.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	cert, key := os.Getenv("BROKK_TOWN_CALLBACK_TLS_CERT"), os.Getenv("BROKK_TOWN_CALLBACK_TLS_KEY")
	if (cert == "") != (key == "") {
		listener.Close()
		return nil, nil, errors.New("agent callback TLS needs both BROKK_TOWN_CALLBACK_TLS_CERT and BROKK_TOWN_CALLBACK_TLS_KEY")
	}
	go func() {
		if cert != "" {
			_ = server.ServeTLS(listener, cert, key)
			return
		}
		_ = server.Serve(listener)
	}()
	return callback, func() { _ = server.Close() }, nil
}

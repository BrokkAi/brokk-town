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
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/botapi/botclient"
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

// townBots resolves each role's bot. Every bot runs in its own container and
// is reached at the URL its environment variable or the config file's bots
// object names, with its token.
func townBots(file map[town.Role]fileBot) (map[town.Role]botapi.Bot, error) {
	bots := map[town.Role]botapi.Bot{}
	caFile := os.Getenv("BROKK_TOWN_BOT_CA_FILE")
	var missing []string
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
			missing = append(missing, fmt.Sprintf("%s (%s)", name, botEnv(role, "URL")))
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
		bots[role] = client
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("Town reaches every bot over the network; set the URL of %s, or name it in the config file's bots object", strings.Join(missing, ", "))
	}
	return bots, nil
}

// agentCallback starts the listener bots use to reach a managed dispatch's
// agent. BROKK_TOWN_CALLBACK_URL is the address bots use, and
// BROKK_TOWN_CALLBACK_LISTEN where the service listens for it (":8091" when
// unset). It returns nil when there is no listener to start.
func agentCallback(ctx context.Context, log *slog.Logger) (*town.AgentCallback, func(), error) {
	advertised := os.Getenv("BROKK_TOWN_CALLBACK_URL")
	listen := os.Getenv("BROKK_TOWN_CALLBACK_LISTEN")
	if advertised == "" {
		log.Warn("BROKK_TOWN_CALLBACK_URL is not set; managed reviews will fail until it is")
		return nil, func() {}, nil
	}
	if listen == "" {
		listen = ":8091"
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, nil, fmt.Errorf("agent callback: %w", err)
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

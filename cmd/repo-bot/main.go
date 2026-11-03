// Command repo-bot serves repo-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	repobot "github.com/BrokkAi/brokk-town/bots/repo-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, repobot.Worker) }

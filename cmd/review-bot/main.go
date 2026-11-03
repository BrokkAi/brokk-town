// Command review-bot serves review-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	reviewbot "github.com/BrokkAi/brokk-town/bots/review-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, reviewbot.Worker) }

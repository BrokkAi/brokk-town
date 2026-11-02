// Command simplifier-bot serves simplifier-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	simplifierbot "github.com/BrokkAi/brokk-town/bots/simplifier-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, simplifierbot.Worker) }

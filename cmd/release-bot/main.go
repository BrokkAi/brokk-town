// Command release-bot serves release-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	releasebot "github.com/BrokkAi/brokk-town/bots/release-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, releasebot.Worker) }

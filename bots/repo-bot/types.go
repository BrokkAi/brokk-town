package repobot

import "github.com/BrokkAi/brokk-town/internal/botapi"

// A repository read reports the observation types of the bot contract
// directly: this bot's result is what crosses to the service, so there is one
// definition of each shape rather than a copy here and a translation there.
type (
	Inventory    = botapi.Inventory
	Issue        = botapi.Issue
	Label        = botapi.Label
	Ref          = botapi.Ref
	Pull         = botapi.Pull
	Release      = botapi.Release
	Commit       = botapi.Commit
	BranchHealth = botapi.BranchHealth
)

// Result is one finished repository run.
type Result struct {
	Inventory *Inventory    `json:"inventory,omitempty"`
	Health    *BranchHealth `json:"health,omitempty"`
}

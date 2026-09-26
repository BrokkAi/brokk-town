package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func choicesCommand(ctx context.Context, dir string, fl *cliFlags, fs *flag.FlagSet) error {
	if !town.ValidRepo(*fl.repo) {
		return errors.New("choices requires --repo OWNER/REPO")
	}
	role := ""
	agent := town.AgentSettings{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "role":
			role = *fl.role
		case "model":
			agent.Model = fl.model
		}
	})
	if role != "" && !town.ValidAgentRole(town.Role(role)) {
		return errors.New("choices requires a bot role, or omit --role for town defaults")
	}
	conn, err := requireService(ctx, dir)
	if err != nil {
		return err
	}
	var result town.AgentChoices
	if err := request(ctx, conn, "POST", "/api/choices", map[string]any{"town": *fl.repo, "role": role, "agent": agent}, &result); err != nil {
		return err
	}
	if *fl.json {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	for _, model := range result.Models {
		fmt.Printf("Model %s · %s\n", model.Value, model.Name)
	}
	for _, effort := range result.Efforts {
		fmt.Printf("Effort %s · %s\n", effort.Value, effort.Name)
	}
	if len(result.Models) == 0 && len(result.Efforts) == 0 {
		fmt.Println("This profile advertises no model or effort selectors; use its defaults.")
	}
	return nil
}

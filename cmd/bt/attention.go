package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/BrokkAi/brokk-town/internal/town"
)

type attentionHookView struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
}

func attentionHookCommand(ctx context.Context, dir string, fl *cliFlags, fs *flag.FlagSet) error {
	if *fl.hookEnable && *fl.hookDisable {
		return errors.New("choose --enable or --disable")
	}
	edit := town.AttentionHookEdit{}
	if *fl.hookEnable || *fl.hookDisable {
		enabled := *fl.hookEnable
		edit.Enabled = &enabled
	}
	commandSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "command-file" {
			commandSet = true
		}
	})
	if commandSet {
		var input io.Reader = os.Stdin
		if *fl.hookCommandFile != "-" {
			f, err := os.Open(*fl.hookCommandFile)
			if err != nil {
				return errors.New("cannot read attention hook command file")
			}
			defer f.Close()
			input = f
		}
		data, err := io.ReadAll(io.LimitReader(input, 64<<10+1))
		var command []string
		if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &command) != nil || command == nil {
			return errors.New("command file must contain one JSON argument array, at most 64 KiB")
		}
		if err := (town.AttentionHook{Command: command}).Validate(); err != nil {
			return err
		}
		edit.Command = &command
	}
	conn, err := requireService(ctx, dir)
	if err != nil {
		return err
	}
	var result attentionHookView
	if edit.Enabled != nil || edit.Command != nil {
		if err := request(ctx, conn, "POST", "/api/attention-hook", edit, &result); err != nil {
			return err
		}
	} else {
		var state struct {
			Service struct {
				Hook attentionHookView `json:"attention_hook"`
			} `json:"service_config"`
		}
		if err := request(ctx, conn, "GET", "/api/state", nil, &state); err != nil {
			return err
		}
		result = state.Service.Hook
	}
	if *fl.json {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("Attention hook: enabled=%t, command configured=%t\n", result.Enabled, result.Configured)
	return nil
}

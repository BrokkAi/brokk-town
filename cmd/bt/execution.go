package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/BrokkAi/brokk-town/internal/mjolnir"
	"github.com/BrokkAi/brokk-town/internal/town"
)

func executionCommand(ctx context.Context, dir string, fl *cliFlags, fs *flag.FlagSet) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	selecting := set["target"] || set["profile"] || *fl.localExecution || *fl.inheritExecution
	if set["runtime-session"] {
		if selecting || *fl.refresh || !town.ValidRepo(*fl.repo) || *fl.executionRuntimeSession == "" || (set["role"] && !town.ValidAgentRole(town.Role(*fl.role))) {
			return errors.New("runtime selection requires --repo OWNER/REPO and --runtime-session ID, with an optional --role BOT; save placement separately")
		}
		conn, err := requireService(ctx, dir)
		if err != nil {
			return err
		}
		role := ""
		if set["role"] {
			role = *fl.role
		}
		var receipt struct {
			OK bool `json:"ok"`
		}
		if err := request(ctx, conn, "POST", "/api/execution-runtime", map[string]any{"town": *fl.repo, "role": role, "session_id": *fl.executionRuntimeSession}, &receipt); err != nil {
			return err
		}
		fmt.Println("Mjolnir runtime selected for future work")
		return nil
	}
	if selecting && *fl.refresh {
		return errors.New("refresh options separately before selecting execution")
	}
	var selection *mjolnir.Selection
	if selecting {
		modes := 0
		if set["target"] || set["profile"] {
			modes++
			selection = &mjolnir.Selection{Target: *fl.executionTarget, Profile: *fl.executionProfile}
			if !selection.Managed() || selection.Validate() != nil {
				return errors.New("select both --target and --profile from bt execution")
			}
		}
		if *fl.localExecution {
			modes++
			selection = &mjolnir.Selection{}
		}
		if *fl.inheritExecution {
			modes++
			if !set["role"] {
				return errors.New("--inherit-execution requires --role BOT")
			}
		}
		if modes != 1 {
			return errors.New("choose one of target/profile, local-execution, or inherit-execution")
		}
		if !town.ValidRepo(*fl.repo) {
			return errors.New("execution selection requires --repo OWNER/REPO")
		}
		if set["role"] && !town.ValidAgentRole(town.Role(*fl.role)) {
			return errors.New("execution selection requires a bot role")
		}
	} else if set["repo"] || set["role"] {
		return errors.New("use --target/--profile, --local-execution, or --inherit-execution with a repository")
	}
	conn, err := requireService(ctx, dir)
	if err != nil {
		return err
	}
	if selecting {
		role := ""
		if set["role"] {
			role = *fl.role
		}
		var receipt struct {
			OK bool `json:"ok"`
		}
		if err := request(ctx, conn, "POST", "/api/execution", map[string]any{"town": *fl.repo, "role": role, "selection": selection}, &receipt); err != nil {
			return err
		}
		fmt.Println("Execution selection saved for future work")
		return nil
	}
	method, path := "GET", "/api/execution-options"
	var body any
	if *fl.refresh {
		method, path, body = "POST", path+"/refresh", map[string]any{}
	}
	var listing mjolnir.Listing
	if err := request(ctx, conn, method, path, body, &listing); err != nil {
		return err
	}
	if *fl.json {
		data, _ := json.MarshalIndent(listing, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if listing.Demo {
		fmt.Println("Demo: Mjolnir is offline")
		return nil
	}
	if !listing.Configured {
		fmt.Println("Mjolnir is not configured; set BT_MJOLNIR_API_URL and BT_MJOLNIR_TOKEN_FILE on the Town service (see mj api-info)")
		return nil
	}
	if *fl.refresh {
		fmt.Println("Refresh requested; run bt execution again for the result")
	}
	if listing.Stale {
		fmt.Println("Cached Mjolnir options are stale")
	}
	if listing.Error != "" {
		fmt.Println(listing.Error)
	}
	if !listing.Fetched.IsZero() {
		fmt.Println("Last fetched:", listing.Fetched.Format("2006-01-02T15:04:05Z07:00"))
	}
	for _, target := range listing.Targets {
		fmt.Printf("Target %s · %s · %s\n", target.ID, target.Kind, target.Availability)
		if target.UnavailableReason != "" {
			fmt.Println(" ", target.UnavailableReason)
		}
	}
	for _, profile := range listing.Profiles {
		fmt.Printf("Profile %s · %s\n", profile.ID, profile.Harness)
	}
	if listing.Default != nil {
		fmt.Printf("Daemon default: %s / %s\n", listing.Default.Target, listing.Default.Profile)
	}
	fmt.Println("Availability is advisory. Mjolnir execution awaits remote checkout and evidence support; selected bots remain held.")
	return nil
}

// Package bundle describes the executables shipped together with Town.
package bundle

import (
	"embed"
	"encoding/json"
)

//go:embed bundle.json
var files embed.FS

type Bot struct {
	Project      string `json:"project"`
	Command      string `json:"command"`
	Version      string `json:"version"`
	SourceCommit string `json:"source_commit"`
}

func Bots() map[string]Bot {
	data, _ := files.ReadFile("bundle.json")
	var v struct {
		Bots map[string]Bot `json:"bots"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		panic(err)
	}
	return v.Bots
}

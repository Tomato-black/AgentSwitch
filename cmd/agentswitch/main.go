package main

import (
	"log"
	"os"
	"path/filepath"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/desktop"
)

func main() {
	base, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(base, "AgentSwitch")
	a, err := app.Open(app.Options{
		DBPath:  filepath.Join(dir, "agentswitch.db"),
		LogPath: filepath.Join(dir, "logs", "app.log"),
		Version: "0.1.0",
		Host:    "127.0.0.1",
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := desktop.Run(a); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/davidparkercodes/belay/cmd/belay/commands"
)

var Version = "v1.2.0"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	root := commands.NewRootCmd(Version)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

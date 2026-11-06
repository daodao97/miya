package controller

import (
	"fmt"
	"log"
	"os"

	"miya/internal/desktop"
	"miya/internal/mcp"
	"miya/internal/workspaces"
)

func RunCommands() bool {
	if len(os.Args) == 2 && os.Args[1] == "-desktop-privacy" {
		if err := desktop.RunPrivacyHelper(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	}
	// Agents Miya runs start it as their MCP server for the conversations.
	if len(os.Args) > 1 && os.Args[1] == "-mcp" {
		if err := mcp.Run(); err != nil {
			log.Fatal(err)
		}
		return true
	}
	if len(os.Args) > 1 && os.Args[1] == "-server" {
		if err := runService(); err != nil {
			log.Fatal(err)
		}
		return true
	}
	if len(os.Args) > 1 && os.Args[1] == "-serve" {
		if err := serveCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return true
	}
	if len(os.Args) == 2 && os.Args[1] == "-connection" {
		data, err := os.ReadFile(ConnectionFile())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(data))
		return true
	}
	if len(os.Args) == 2 && os.Args[1] == "-restart-when-idle" {
		if err := requestServerRestart(); err != nil {
			log.Fatal(err)
		}
		return true
	}
	if len(os.Args) > 1 && os.Args[1] == "-connect" {
		if len(os.Args) != 3 {
			log.Fatal("usage: miya -connect <miya://connect link>")
		}
		if err := workspaces.SaveRemoteServer(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		os.Args = os.Args[:1]
	}

	return false
}

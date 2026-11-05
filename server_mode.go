package main

import (
	"flag"
	"fmt"
	"miya/internal/appdir"
	"miya/internal/fsutil"
	"os"
	"path/filepath"
)

// -server remains the window-managed background process. -serve is a
// permanent installation with desktop and phone access over Tailcat.
type serverOptions struct{ Persistent bool }

var servingRemote bool

func serveCommand(args []string) error {
	flags := flag.NewFlagSet("miya -serve", flag.ContinueOnError)
	data := flags.String("data-dir", "", "service and pairing state directory (also MIYA_DIR)")
	workspace := flags.String("workspace", "", "initial project directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *data == "" {
		*data = appdir.Dir()
	}
	if *data != "" {
		abs, err := filepath.Abs(*data)
		if err != nil {
			return err
		}
		if err := os.Setenv("MIYA_DIR", abs); err != nil {
			return err
		}
	}
	if *workspace != "" {
		abs, err := filepath.Abs(*workspace)
		if err != nil {
			return err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("workspace is not a directory: %s", abs)
		}
		if err := os.Chdir(abs); err != nil {
			return err
		}
		if err := os.Setenv("QUICKGUI_WORKSPACE", abs); err != nil {
			return err
		}
	}
	return runServiceWithOptions(serverOptions{Persistent: true})
}

func connectionFile() string { return filepath.Join(appdir.Dir(), "connection.txt") }

func requestServerRestart() error {
	cl, err := dialService(serviceSocket())
	if err != nil {
		return err
	}
	defer cl.Close()
	// A no-reply request avoids mistaking a quick, idle shutdown for a
	// failed RPC. The supervising service manager starts the replacement.
	return cl.Send(svcRequest{Op: "quitWhenIdle"})
}

func saveServerConnection(link string) error {
	if err := os.MkdirAll(appdir.Dir(), 0700); err != nil {
		return err
	}
	return fsutil.WritePrivateFile(connectionFile(), []byte(link+"\n"))
}

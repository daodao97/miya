package main

import (
	"flag"
	"fmt"
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
		*data = serviceDir()
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

func connectionFile() string { return filepath.Join(serviceDir(), "connection.txt") }

func requestServerRestart() error {
	cl, err := dialService(serviceSocket())
	if err != nil {
		return err
	}
	defer cl.Close()
	// A no-reply request avoids mistaking a quick, idle shutdown for a
	// failed RPC. The supervising service manager starts the replacement.
	return cl.send(svcRequest{Op: "quitWhenIdle"})
}

func saveServerConnection(link string) error {
	if err := os.MkdirAll(serviceDir(), 0700); err != nil {
		return err
	}
	return writePrivateFile(connectionFile(), []byte(link+"\n"))
}

// Rename a complete 0600 file, including when replacing an older file with
// broader permissions. Pairing capabilities must never enter shared logs.
func writePrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".miya-private-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

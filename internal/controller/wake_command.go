package controller

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"miya/internal/automation"
	svcclient "miya/internal/service/client"
	"miya/internal/service/process"
	svcproto "miya/internal/service/protocol"
)

const wakeUsage = "usage: miya -wake <automation id or name> [-trigger id] [-data text|@file|-] [-dedupe key]"

// wakeCommand wakes an automation through its ingress trigger from a
// terminal, a script, a Git hook or another scheduler, by the background
// service's socket. It starts the service when it is not running.
func wakeCommand(args []string) error {
	fs := flag.NewFlagSet("wake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	trigger := fs.String("trigger", "", "")
	data := fs.String("data", "", "")
	dedupe := fs.String("dedupe", "", "")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(wakeUsage)
	}
	ref := args[0]
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return errors.New(wakeUsage)
	}
	payload, err := wakeData(*data)
	if err != nil {
		return err
	}
	cl, err := dialService()
	if err != nil {
		return err
	}
	defer cl.Close()
	var w automation.Wake
	if err := cl.Call("automationWake", automation.Request{ID: ref, Trigger: *trigger, Data: payload, Dedupe: *dedupe}, &w); err != nil {
		return err
	}
	line := automation.OutcomeLabel(w.Outcome)
	if w.Detail != "" {
		line += ": " + w.Detail
	}
	fmt.Println(line)
	return nil
}

// wakeData reads what a wake brings: text, @file, or - for standard input.
func wakeData(v string) (string, error) {
	var b []byte
	var err error
	switch {
	case v == "-":
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	case strings.HasPrefix(v, "@"):
		b, err = os.ReadFile(strings.TrimPrefix(v, "@"))
	default:
		return v, nil
	}
	return string(b), err
}

// dialService connects to the background service, starting it when none
// answers.
func dialService() (*svcclient.Client, error) {
	sock := svcproto.Socket()
	if cl, err := svcclient.Dial(sock); err == nil {
		return cl, nil
	}
	workspace, err := os.UserHomeDir()
	if err != nil {
		workspace = "/"
	}
	if err := process.Spawn(workspace); err != nil {
		return nil, fmt.Errorf("starting the Miya background service: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		cl, err := svcclient.Dial(sock)
		if err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the Miya background service did not start: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

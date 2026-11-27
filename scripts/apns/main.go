// Command apns configures the APNs sender used by the Miya service.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/apns"
	"miya/internal/appdir"
	"miya/internal/notification"
)

func main() {
	if err := run(os.Args[1:]); errors.Is(err, flag.ErrHelp) {
		return
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: go run ./scripts/apns {setup|status} -h")
	}
	switch args[0] {
	case "setup":
		return setup(args[1:])
	case "status":
		flags := flag.NewFlagSet("status", flag.ContinueOnError)
		dir := flags.String("data-dir", appdir.Dir(), "Miya service data directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("status takes no positional arguments")
		}
		return status(*dir)
	default:
		return errors.New("usage: go run ./scripts/apns {setup|status} -h")
	}
}

func setup(args []string) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	dir := flags.String("data-dir", appdir.Dir(), "Miya service data directory")
	team := flags.String("team", "", "Apple Developer Team ID")
	environment := flags.String("environment", string(apns.Sandbox), "APNs environment: sandbox or production")
	keyID := flags.String("key-id", "", "Apple key ID (defaults to the ID in AuthKey_KEYID.p8)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: go run ./scripts/apns setup [flags] /path/to/AuthKey_KEYID.p8")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *team == "" || flags.NArg() != 1 {
		flags.Usage()
		return errors.New("setup requires -team and one APNs .p8 file")
	}
	if *keyID == "" {
		name := filepath.Base(flags.Arg(0))
		if !strings.HasPrefix(name, "AuthKey_") || !strings.HasSuffix(name, ".p8") {
			return errors.New("pass -key-id for a renamed Apple key file")
		}
		*keyID = strings.TrimSuffix(strings.TrimPrefix(name, "AuthKey_"), ".p8")
	}
	keyFile, err := os.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	defer keyFile.Close()
	key, err := io.ReadAll(io.LimitReader(keyFile, 8193))
	if err != nil {
		return err
	}
	cfg := apns.ProviderConfig{
		TeamID: *team, KeyID: *keyID, Topic: notification.APNsTopic, Environment: apns.Environment(*environment),
	}
	if err := apns.ImportProvider(*dir, cfg, key); err != nil {
		return err
	}
	return status(*dir)
}

func status(dir string) error {
	p, err := notification.OpenAPNsProvider(dir)
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	fmt.Printf("APNs credentials loaded locally for %s (%s).\n", p.Topic(), dir)
	return nil
}

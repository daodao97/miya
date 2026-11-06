package cli

import "errors"

func Location(agent string) (binary, home string, err error) {
	binary, err = FindBinary(agent)
	if err != nil {
		return "", "", errors.New("Could not find " + agent + " command")
	}
	home, err = ConfigDir(agent)
	if err != nil {
		return "", "", errors.New("Unable to read the local configuration directory")
	}
	return binary, home, nil
}

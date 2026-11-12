//go:build darwin && !ios

package privilege

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// SessionCommand has no paths, credentials or executable arguments. Country
// changes operate only on the authenticated helper's existing tinc session.
type SessionCommand struct {
	Action  string `json:"action"`
	Country string `json:"country,omitempty"`
}

var sessionCountry = regexp.MustCompile(`^[A-Z]{2}$`)

func parseSessionCommand(line string) (SessionCommand, error) {
	line = strings.TrimSpace(line)
	if line == "stop" {
		return SessionCommand{Action: "stop"}, nil
	}
	var command SessionCommand
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil {
		return SessionCommand{}, errors.New("invalid session command")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return SessionCommand{}, errors.New("invalid session command")
	}
	switch command.Action {
	case "country":
		if !sessionCountry.MatchString(command.Country) {
			return SessionCommand{}, errors.New("invalid session country")
		}
	case "stop", "collect":
		if command.Country != "" {
			return SessionCommand{}, errors.New("unexpected session country")
		}
	default:
		return SessionCommand{}, errors.New("unsupported session command")
	}
	return command, nil
}

// ReadSessionCommands bounds every command and stops on explicit stop, EOF or
// malformed input. The caller decides whether diagnostics are enabled.
func ReadSessionCommands(reader io.Reader, consume func(SessionCommand) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 256), 1024)
	for scanner.Scan() {
		command, err := parseSessionCommand(scanner.Text())
		if err != nil {
			return err
		}
		if err := consume(command); err != nil {
			return err
		}
		if command.Action == "stop" {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

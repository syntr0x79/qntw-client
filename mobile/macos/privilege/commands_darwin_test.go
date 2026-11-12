//go:build darwin && !ios

package privilege

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSessionCountryCommandsKeepOneChannelUntilStop(t *testing.T) {
	var got []SessionCommand
	err := ReadSessionCommands(strings.NewReader("{\"action\":\"country\",\"country\":\"SE\"}\n{\"action\":\"collect\"}\nstop\n"), func(c SessionCommand) error {
		got = append(got, c)
		return nil
	})
	want := []SessionCommand{{Action: "country", Country: "SE"}, {Action: "collect"}, {Action: "stop"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("commands: %#v, %v", got, err)
	}
}

func TestSessionCommandRejectsUnknownFieldsAndUnboundedInput(t *testing.T) {
	for _, input := range []string{
		`{"action":"country","country":"SE","path":"/tmp/other"}`,
		`{"action":"country","country":"SE\nstop"}`,
		`{"action":"country","country":"se"}`,
		`{"action":"stop","country":"SE"}`,
		`{"action":"exec"}`,
		`{"action":"stop"} {"action":"country","country":"SE"}`,
		strings.Repeat("x", 2048),
	} {
		t.Run(input[:min(len(input), 40)], func(t *testing.T) {
			called := false
			err := ReadSessionCommands(strings.NewReader(input+"\n"), func(SessionCommand) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("invalid command consumed: %v", err)
			}
		})
	}
}

func TestSessionCommandEOFCancelsAndConsumerErrorsPropagate(t *testing.T) {
	if err := ReadSessionCommands(strings.NewReader(""), func(SessionCommand) error { return nil }); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}
	want := errors.New("consumer stopped")
	if err := ReadSessionCommands(strings.NewReader("{\"action\":\"country\",\"country\":\"PL\"}\n"), func(SessionCommand) error { return want }); !errors.Is(err, want) {
		t.Fatalf("consumer failure: %v", err)
	}
}

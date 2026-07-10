package relay

import (
	"errors"
	"strings"
	"testing"
)

func TestReadRelayBodyLimit(t *testing.T) {
	body, err := readRelayBody(strings.NewReader("1234"), 4)
	if err != nil || string(body) != "1234" {
		t.Fatalf("readRelayBody within limit = %q, %v", body, err)
	}
	if _, err := readRelayBody(strings.NewReader("12345"), 4); !errors.Is(err, errRelayBodyTooLarge) {
		t.Fatalf("expected errRelayBodyTooLarge, got %v", err)
	}
}

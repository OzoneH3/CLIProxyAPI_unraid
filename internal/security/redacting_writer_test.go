package security

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactingWriter(t *testing.T) {
	var out bytes.Buffer
	w := NewRedactingWriter(&out)
	if _, err := w.Write([]byte("safe startup\nAuthorization: Bearer secret\n")); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "safe startup") || strings.Contains(got, "secret") || !strings.Contains(got, "sensitive log line omitted") {
		t.Fatalf("unexpected output: %q", got)
	}
}

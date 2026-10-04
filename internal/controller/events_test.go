package controller

import (
	"strings"
	"testing"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
)

// recordedNote sends note through NamespaceReconciler.event and returns the note the recorder got.
func recordedNote(t *testing.T, note string) string {
	t.Helper()
	recorder := &events.FakeRecorder{Events: make(chan string, 1)}
	r := &NamespaceReconciler{Recorder: recorder}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns"}}
	r.event(ns, nil, corev1.EventTypeWarning, ReasonApplyFailed, "Apply", note)

	prefix := corev1.EventTypeWarning + " " + ReasonApplyFailed + " "
	got := <-recorder.Events
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("recorded %q, want the prefix %q", got, prefix)
	}
	return strings.TrimPrefix(got, prefix)
}

func TestEventNoteTruncation(t *testing.T) {
	t.Run("short note is kept as it is, including %", func(t *testing.T) {
		note := `cannot apply ConfigMap/a: 100% of "%s" and %d`
		if got := recordedNote(t, note); got != note {
			t.Errorf("note = %q, want %q", got, note)
		}
	})

	t.Run("note of exactly the limit is kept", func(t *testing.T) {
		note := strings.Repeat("a", maxNoteBytes)
		if got := recordedNote(t, note); got != note {
			t.Errorf("note of %d bytes was changed to %d bytes", len(note), len(got))
		}
	})

	t.Run("line breaks are joined", func(t *testing.T) {
		note := "first problem\nsecond problem"
		if got, want := recordedNote(t, note), "first problem; second problem"; got != want {
			t.Errorf("note = %q, want %q", got, want)
		}
	})

	t.Run("long note is cut", func(t *testing.T) {
		note := strings.Repeat("x", 5000)
		got := recordedNote(t, note)
		if len(got) > maxNoteBytes {
			t.Errorf("note has %d bytes, want at most %d", len(got), maxNoteBytes)
		}
		if !strings.HasPrefix(note, strings.TrimSuffix(got, "...")) || !strings.HasSuffix(got, "...") {
			t.Errorf("note = %q, want the start of the original note followed by ...", got)
		}
	})

	t.Run("long note is cut between characters", func(t *testing.T) {
		// "€" takes 3 bytes, so a cut at byte 997 would split one.
		note := strings.Repeat("€", 1000)
		got := recordedNote(t, note)
		if len(got) > maxNoteBytes {
			t.Errorf("note has %d bytes, want at most %d", len(got), maxNoteBytes)
		}
		if !utf8.ValidString(got) {
			t.Errorf("note is not valid UTF-8: %q", got)
		}
	})
}

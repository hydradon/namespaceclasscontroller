package controller

import (
	"strings"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/runtime"
)

// Event reasons. Created, Updated and Deleted are Normal events; the others are Warnings.
const (
	ReasonCreated           = "Created"
	ReasonUpdated           = "Updated"
	ReasonDeleted           = "Deleted"
	ReasonClassNotFound     = "ClassNotFound"
	ReasonInvalidClass      = "InvalidClass"
	ReasonInvalidAnnotation = "InvalidAnnotation"
	ReasonConflict          = "Conflict"
	ReasonApplyFailed       = "ApplyFailed"
	ReasonDeleteFailed      = "DeleteFailed"
)

// Event actions: what the controller was doing when it recorded the event.
const (
	actionReconcile = "Reconcile"
	actionApply     = "Apply"
	actionDelete    = "Delete"
)

// maxNoteBytes keeps event notes under the API server limit of 1024 bytes.
const maxNoteBytes = 1000

// event records an event about regarding. related is the object the event is about, or a literal
// nil. The recorder merges events with the same type, reason, action, regarding and related
// objects into one and keeps the first note, so related must name the specific object.
func (r *NamespaceReconciler) event(regarding, related runtime.Object, eventType, reason, action, note string) {
	r.Recorder.Eventf(regarding, related, eventType, reason, action, "%s", shortNote(note))
}

// shortNote puts the note on one line and cuts it to maxNoteBytes without splitting a character.
func shortNote(note string) string {
	note = strings.ReplaceAll(note, "\n", "; ")
	if len(note) <= maxNoteBytes {
		return note
	}
	const ellipsis = "..."
	cut := maxNoteBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(note[cut]) {
		cut--
	}
	return note[:cut] + ellipsis
}

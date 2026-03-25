package recorder

import "errors"

// ErrNoRecordingsFound is returned when the recording index has no entries
// matching the requested channel and time range.
var ErrNoRecordingsFound = errors.New("recorder: no recordings found")

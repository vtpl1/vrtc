// Package schedule provides types and interfaces for reading recording
// schedules from pluggable sources (JSON file, database, etc.).
//
// Core types are defined in the SDK (github.com/vtpl1/vrtc-sdk/config/schedule)
// and re-exported here for backward compatibility. Provider implementations
// (file, MySQL, MongoDB) remain in this package.
package schedule

import sdkschedule "github.com/vtpl1/vrtc-sdk/config/schedule"

// Schedule describes one recording directive.
type Schedule = sdkschedule.Schedule

// ScheduleProvider is the single interface all schedule sources must satisfy.
type ScheduleProvider = sdkschedule.ScheduleProvider

// IsActive reports whether s should be recording at time now.
var IsActive = sdkschedule.IsActive //nolint:gochecknoglobals

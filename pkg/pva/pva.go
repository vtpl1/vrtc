// Package pva defines per-frame analytics types produced by object-detection
// pipelines and attached to av.Packet.PVAData in the streaming pipeline.
package pva

import (
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
)

// ObjectInfo is an alias for av.ObjectInfo.
type ObjectInfo = av.ObjectInfo

// PVAData is an alias for av.PVAData.
// Callers may use either pva.PVAData or av.PVAData — they are the same type.
type PVAData = av.PVAData

// Source is the interface implemented by any component that can supply
// PVAData for a given frame. The merger calls Fetch on every packet;
// return nil when no analytics are available for that frame.
type Source interface {
	Fetch(frameID int64, wallClock time.Time) *PVAData
}

// NilSource is a Source that always returns nil.
// Use it when analytics are not connected.
type NilSource struct{}

func (NilSource) Fetch(_ int64, _ time.Time) *PVAData { return nil }

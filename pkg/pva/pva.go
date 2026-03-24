// Package pva defines per-frame analytics types produced by object-detection
// pipelines and attached to av.Packet.Metadata in the streaming pipeline.
//
// This package has no dependency on pkg/av, which keeps av.Packet free of a
// circular import while still carrying a typed analytics payload via its
// Metadata any field.
package pva

import (
	"encoding/json"
	"time"
)

// ObjectInfo represents a single detected object within a video frame.
type ObjectInfo struct {
	X uint32 `bson:"x" json:"x"`
	Y uint32 `bson:"y" json:"y"`
	W uint32 `bson:"w" json:"w"`
	H uint32 `bson:"h" json:"h"`
	T uint32 `bson:"t" json:"t"` // object type / class id
	C uint32 `bson:"c" json:"c"` // confidence score
	I int64  `bson:"i" json:"i"` // object instance id
	E bool   `bson:"e" json:"e"` // event flag
}

// PVAData carries object-detection analytics associated with a single video frame.
// A nil *PVAData means analytics are absent for that frame.
type PVAData struct {
	SiteID           int32         `bson:"siteId"           json:"siteId"`
	ChannelID        int32         `bson:"channelId"        json:"channelId"`
	StartTimestamp   int64         `bson:"timeStamp"        json:"timeStamp"`
	EndTimestamp     int64         `bson:"timeStampEnd"     json:"timeStampEnd"`
	EncodedTimestamp int64         `bson:"timeStampEncoded" json:"timeStampEncoded"`
	FrameID          int64         `bson:"frameId"          json:"frameId"`
	VehicleCount     int32         `bson:"vehicleCount"     json:"vehicleCount"`
	PeopleCount      int32         `bson:"peopleCount"      json:"peopleCount"`
	RefWidth         int32         `bson:"refWidth"         json:"refWidth"`
	RefHeight        int32         `bson:"refHeight"        json:"refHeight"`
	Objects          []*ObjectInfo `bson:"objectList"       json:"objectList,omitempty"`
}

func (p *PVAData) String() string {
	if p == nil {
		return "nil"
	}

	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}

	return string(b)
}

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

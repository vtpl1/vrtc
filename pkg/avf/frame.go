package avf

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vtpl1/vrtc/pkg/av"
	"github.com/vtpl1/vrtc/pkg/av/codec/h264parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/h265parser"
	"github.com/vtpl1/vrtc/pkg/av/codec/parser"
)

type (
	MediaType uint32
	FrameType uint32
)

const (
	MJPG       MediaType = 0
	MPEG       MediaType = 1
	H264       MediaType = 2
	MLAW       MediaType = 3
	PCMU       MediaType = 3
	PCM_MU_LAW MediaType = 3
	PCM        MediaType = 3
	G711       MediaType = 3
	G711U      MediaType = 3
	ALAW       MediaType = 4
	PCMA       MediaType = 4
	PCM_ALAW   MediaType = 4
	G711A      MediaType = 4
	L16        MediaType = 5
	ACC        MediaType = 6
	AAC        MediaType = 6
	UNKNOWN    MediaType = 7
	H265       MediaType = 8
	G722       MediaType = 9
	G726       MediaType = 10
	OPUS       MediaType = 11
	MP2L2      MediaType = 12
)

const (
	H_FRAME        FrameType = 0
	I_FRAME        FrameType = 1
	P_FRAME        FrameType = 2
	CONNECT_HEADER FrameType = 3
	AUDIO_FRAME    FrameType = 16
	UNKNOWN_FRAME  FrameType = 17
)

type ObjectInfo struct {
	X uint32 `bson:"x" json:"x"`
	Y uint32 `bson:"y" json:"y"`
	W uint32 `bson:"w" json:"w"`
	H uint32 `bson:"h" json:"h"`
	T uint32 `bson:"t" json:"t"`
	C uint32 `bson:"c" json:"c"`
	I int64  `bson:"i" json:"i"`
	E bool   `bson:"e" json:"e"`
}

type PVAData struct {
	SiteID           int32        `bson:"siteId"           json:"siteId"`
	ChannelID        int32        `bson:"channelId"        json:"channelId"`
	StartTimestamp   int64        `bson:"timeStamp"        json:"timeStamp"`
	EndTimestamp     int64        `bson:"timeStampEnd"     json:"timeStampEnd"`
	EncodedTimestamp int64        `bson:"timeStampEncoded" json:"timeStampEncoded"`
	FrameID          uint64       `bson:"frameId"          json:"frameId"`
	VehicleCount     int32        `bson:"vehicleCount"     json:"vehicleCount"`
	PeopleCount      int32        `bson:"peopleCount"      json:"peopleCount"`
	RefWidth         int32        `bson:"refWidth"         json:"refWidth"`
	RefHeight        int32        `bson:"refHeight"        json:"refHeight"`
	ObjectList       []ObjectInfo `bson:"objectList"       json:"objectList,omitempty"`
}

func (obj PVAData) String() string {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return ""
	}

	return string(jsonBytes)
}

type BasicFrame struct {
	MediaType MediaType
	FrameType FrameType
	TimeStamp int64 // timestamp in milliseconds
	FrameSize uint32
}

type Frame struct {
	BasicFrame

	TotalSize       uint32
	Bitrate         int32
	Fps             int32
	MotionAvailable int8
	StreamType      int8
	ChannelID       int16
	SSrc            uint32
	DurationMs      int64
	FrameID         int64
	RefFrameOff     int64
	CurrentFrameOff int64
	Data            []byte
	Pvadata         PVAData
}

func (m *Frame) CodecType() av.CodecType {
	var codecType av.CodecType

	switch m.MediaType {
	case MJPG:
		codecType = av.MJPEG
	case MPEG:
		codecType = av.UNKNOWN
	case H264:
		codecType = av.H264
	case PCM_MU_LAW:
		codecType = av.PCM_MULAW
	case PCM_ALAW:
		codecType = av.PCM_ALAW
	case L16:
		codecType = av.PCML
	case AAC:
		codecType = av.AAC
	case UNKNOWN:
		codecType = av.UNKNOWN
	case H265:
		codecType = av.H265
	case G722:
		codecType = av.UNKNOWN
	case G726:
		codecType = av.UNKNOWN
	case OPUS:
		codecType = av.OPUS
	case MP2L2:
		codecType = av.UNKNOWN
	}

	return codecType
}

func (m *Frame) IsAudio() bool {
	switch m.MediaType {
	case PCM_MU_LAW, PCM_ALAW, L16, AAC, G722, G726, OPUS, MP2L2:
		return true
	default:
		return false
	}
}

func (m *Frame) IsKeyFrame() bool {
	return m.FrameType == I_FRAME
}

func (m *Frame) IsDataNALU() bool {
	return (m.FrameType == I_FRAME || m.FrameType == P_FRAME)
}

func (m *Frame) IsVideo() bool {
	switch m.MediaType {
	case MJPG, MPEG, H264, H265:
		return true
	default:
		return false
	}
}

func (m *Frame) String() string {
	var naluStr string

	switch {
	case m.FrameType == AUDIO_FRAME:
		naluStr = "AUDIO"
	case len(m.Data) > 4:
		switch m.MediaType {
		case H265:
			nalu := av.H265NaluType(m.Data[0]>>1) & av.H265NALTypeMask
			naluStr = nalu.String()
		case H264:
			nalu := av.H264NaluType(m.Data[0]) & av.H264NALTypeMask
			naluStr = nalu.String()
		}
	default:
		naluStr = "EMPTY"
	}

	return fmt.Sprintf(
		"ID=%d Time=%dms Media=%s NALU=%s Duration=%d ms DataLen=%d",
		m.FrameID,
		m.TimeStamp,
		m.CodecType().String(),
		naluStr,
		m.DurationMs,
		len(m.Data),
	)
}

func MediaTypeFromCodec(codec av.CodecType) MediaType {
	switch codec {
	case av.H264:
		return H264
	case av.H265:
		return H265
	case av.JPEG:
		return MJPG
	case av.VP8:
		return UNKNOWN
	case av.VP9:
		return UNKNOWN
	case av.AV1:
		return UNKNOWN
	case av.MJPEG:
		return MJPG
	case av.AAC:
		return AAC
	case av.PCM_MULAW:
		return PCM_MU_LAW
	case av.PCM_ALAW:
		return PCM_ALAW
	case av.SPEEX:
		return UNKNOWN
	case av.PCM:
		return PCM
	case av.OPUS:
		return OPUS
	}

	return UNKNOWN
}

func FrameTypeFromPktData(data []byte, codec av.CodecType) FrameType {
	switch codec {
	case av.H264:
		if h264parser.IsKeyFrame(data) {
			return I_FRAME
		}

		if h264parser.IsSPSNALU(data) {
			return CONNECT_HEADER
		}

		if h264parser.IsPPSNALU(data) {
			return CONNECT_HEADER
		}

		if h264parser.IsDataNALU(data) {
			return P_FRAME
		}

		return H_FRAME
	case av.H265:
		if h265parser.IsKeyFrame(data) {
			return I_FRAME
		}

		if h265parser.IsVPSNALU(data) {
			return CONNECT_HEADER
		}

		if h265parser.IsSPSNALU(data) {
			return CONNECT_HEADER
		}

		if h265parser.IsPPSNALU(data) {
			return CONNECT_HEADER
		}

		if h265parser.IsDataNALU(data) {
			return P_FRAME
		}

		return H_FRAME
	case av.JPEG:
		return I_FRAME
	// case av.VP8:
	// case av.VP9:
	// case av.AV1:
	case av.MJPEG:
		return I_FRAME
	case av.AAC:
		return AUDIO_FRAME
	case av.PCM_MULAW:
		return AUDIO_FRAME
	case av.PCM_ALAW:
		return AUDIO_FRAME
	case av.SPEEX:
		return AUDIO_FRAME
	case av.PCM:
		return AUDIO_FRAME
	}

	return H_FRAME
}

func FrameToAVPacket(frame *Frame) *av.Packet {
	var (
		data []byte
		idx  uint16
	)

	if frame.FrameType == AUDIO_FRAME {
		idx = 1
		data = frame.Data
	} else {
		data = frame.Data[4:]
	}

	pkt := av.Packet{
		KeyFrame:       frame.FrameType == I_FRAME,
		Idx:            idx,
		WallClockTime:  time.UnixMilli(frame.TimeStamp),
		Data:           data,
		Extra:          frame.Pvadata,
		FrameID:        frame.FrameID,
		CodecType:      frame.CodecType(),
		IsParamSetNALU: frame.FrameType == CONNECT_HEADER,
		Duration:       time.Duration(frame.DurationMs) * time.Millisecond,
	}

	return &pkt
}

func AVPacketToFrame(pkt *av.Packet) *Frame {
	data := pkt.Data

	switch pkt.CodecType {
	case av.H264:
		data = append(parser.StartCode4, pkt.Data...) //nolint:gocritic
	case av.H265:
		data = append(parser.StartCode4, pkt.Data...) //nolint:gocritic
	}

	metadata := PVAData{
		StartTimestamp: pkt.WallClockTime.UnixMilli(),
		EndTimestamp:   pkt.WallClockTime.UnixMilli(),
	}
	pvadata, ok := pkt.Extra.(PVAData)

	if ok {
		metadata = pvadata
	}

	frame := Frame{
		BasicFrame: BasicFrame{
			MediaType: MediaTypeFromCodec(pkt.CodecType),
			FrameType: FrameTypeFromPktData(pkt.Data, pkt.CodecType),
			TimeStamp: pkt.WallClockTime.UnixMilli(),
			FrameSize: uint32(len(data)),
		},
		FrameID: pkt.FrameID,
		Data:    data,
		Pvadata: metadata,
	}

	return &frame
}

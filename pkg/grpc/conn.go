package grpc

import "github.com/vtpl1/vrtc/pkg/core"

type Conn struct {
	core.Connection
	core.Listener

	uri string
}

// GetMedias implements core.Producer.
// Subtle: this method shadows the method (Connection).GetMedias of Conn.Connection.
func (c *Conn) GetMedias() []*core.Media {
	return c.Medias
}

// GetTrack implements core.Producer.
// Subtle: this method shadows the method (Connection).GetTrack of Conn.Connection.
func (c *Conn) GetTrack(media *core.Media, codec *core.Codec) (*core.Receiver, error) {
	core.Assert(media.Direction == core.DirectionRecvonly)

	for _, track := range c.Receivers {
		if track.Codec == codec {
			return track, nil
		}
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.state == StatePlay {
		if err := c.Reconnect(); err != nil {
			return nil, err
		}
	}

	channel, err := c.SetupMedia(media)
	if err != nil {
		return nil, err
	}

	c.state = StateSetup

	track := core.NewReceiver(media, codec)
	track.ID = channel
	c.Receivers = append(c.Receivers, track)

	return track, nil
}

// Start implements core.Producer.
func (c *Conn) Start() error {
	panic("unimplemented")
}

// Stop implements core.Producer.
// Subtle: this method shadows the method (Connection).Stop of Conn.Connection.
func (c *Conn) Stop() error {
	panic("unimplemented")
}

package videonetics

import (
	"context"
	"strconv"
	"sync"

	"github.com/vtpl1/vrtc/pkg/core"
	pb "github.com/vtpl1/vrtc/pkg/videonetics/service"
	"google.golang.org/grpc"
)

type Conn struct {
	core.Connection

	// internal
	uri     string
	ctx     *context.Context
	conn    *grpc.ClientConn
	host    string
	channel Channel

	state   State
	stateMu sync.Mutex
	stream  pb.StreamService_ReadFramePVAClient
}

const (
	MethodSetup = "SETUP"
	MethodPlay  = "PLAY"
)

type State byte

func (s State) String() string {
	switch s {
	case StateNone:
		return "NONE"
	case StateConn:
		return "CONN"
	case StatePlay:
		return MethodPlay
	}
	return strconv.Itoa(int(s))
}

const (
	StateNone State = iota
	StateConn
	StatePlay
)

func (c *Conn) Handle() (err error) {
	return c.ReadFramePVA()
}

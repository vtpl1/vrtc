package avf

import "context"

type FrameMuxerFactory func(ctx context.Context, sourceID, producerID string) (FrameMuxCloser, error)

type AVFFrameMuxerRemover func(ctx context.Context, sourceID, producerID string) error

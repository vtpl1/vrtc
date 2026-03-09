package mp4_test

import (
	"bytes"
	"testing"

	"github.com/vtpl1/vrtc/pkg/av"
)

func TestMP4Moov_NoMvex(t *testing.T) {
	t.Parallel()

	// Non-fragmented MP4 must NOT contain an mvex box.
	h264 := makeH264Codec(t)
	data := muxFmt(t, allFormats[2], []av.Stream{{Idx: 0, Codec: h264}}, nil)

	if bytes.Contains(data, []byte("mvex")) {
		t.Error("non-fragmented mp4 moov must not contain mvex")
	}
}

func TestMP4Moov_ContainsAvcC(t *testing.T) {
	t.Parallel()

	h264 := makeH264Codec(t)
	data := muxFmt(t, allFormats[2], []av.Stream{{Idx: 0, Codec: h264}}, nil)

	if !bytes.Contains(data, []byte("avcC")) {
		t.Error("mp4 moov does not contain avcC box")
	}
}

func TestMP4Moov_ContainsEsds(t *testing.T) {
	t.Parallel()

	aac := makeAACCodec(t)
	data := muxFmt(t, allFormats[2], []av.Stream{{Idx: 0, Codec: aac}}, nil)

	if !bytes.Contains(data, []byte("esds")) {
		t.Error("mp4 moov does not contain esds box")
	}
}

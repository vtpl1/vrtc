package grpc

import (
	"context"

	"github.com/rs/zerolog/log"
	pb "github.com/vtpl1/vrtc/pkg/grpc/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func runDataGen(ctx *context.Context, metadataAddr string) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	conn, err := grpc.NewClient(metadataAddr, opts...)
	if err != nil {
		log.Info().Msg("[" + metadataAddr + "] failed to dial: " + err.Error() + " for")
		return
	}
	defer conn.Close()
	log.Info().Msg("[" + metadataAddr + "] success to dial for ")
	client := pb.NewStreamServiceClient(conn)
	stream, err := client.WritePVAData(*ctx)
	if err != nil {
		log.Error().Msg("[" + metadataAddr + "] failed to WritePVAData: " + err.Error())
		return
	}
	var siteId int32 = 1
	var channelId int32 = 1
	err = stream.Send(&pb.WritePVADataRequest{
		Channel: &pb.Channel{
			SiteId:    int64(siteId),
			ChannelId: int64(channelId),
			AppId:     0,
		},
		PvaData: &pb.PVAData{
			SiteId:    siteId,
			ChannelId: channelId,
		},
	})
	if err != nil {
		log.Error().Msg("[" + metadataAddr + "] failed to int32: " + err.Error())
		return
	}
}

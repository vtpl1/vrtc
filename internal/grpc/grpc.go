package grpc

import (
	"context"

	"github.com/vtpl1/vrtc/internal/app"
	pb "github.com/vtpl1/vrtc/internal/grpc/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func Init(ctx *context.Context) {
	var cfg struct {
		Mod struct {
			StreamAddr   string `yaml:"stream_addr"`
			MetadataAddr string `yaml:"metadata_addr"`
		} `yaml:"api"`
	}
	// default config
	cfg.Mod.StreamAddr = "dns:///172.16.2.143:2003"

	go runDataGen(ctx, cfg.Mod.MetadataAddr)

}

func runDataGen(ctx *context.Context, metadataAddr string) {
	log := app.GetLogger("api")
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

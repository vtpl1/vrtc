package channel

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoProvider implements ChannelProvider using a MongoDB collection.
//
// Expected document shape:
//
//	{
//	  "_id":        "cam-1",
//	  "name":       "Front Door",
//	  "stream_url": "rtsp://10.0.0.1/main",
//	  "username":   "admin",
//	  "password":   "secret",
//	  "site_id":    1,
//	  "extra":      { "key": "value" }
//	}
type mongoProvider struct {
	coll *mongo.Collection
}

// NewMongoProvider returns a ChannelProvider backed by the given MongoDB collection.
// The caller owns the client and is responsible for disconnecting it.
func NewMongoProvider(coll *mongo.Collection) ChannelProvider {
	return &mongoProvider{coll: coll}
}

// mongoChannel is the BSON document representation of a Channel.
type mongoChannel struct {
	ID        string            `bson:"_id"`
	Name      string            `bson:"name"`
	StreamURL string            `bson:"stream_url"`
	Username  string            `bson:"username"`
	Password  string            `bson:"password"`
	SiteID    int               `bson:"site_id"`
	Extra     map[string]string `bson:"extra"`
}

func (p *mongoProvider) GetChannel(ctx context.Context, id string) (Channel, error) {
	var doc mongoChannel

	err := p.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Channel{}, fmt.Errorf("%w: %s", ErrChannelNotFound, id)
	}

	if err != nil {
		return Channel{}, fmt.Errorf("channel mongo: get %q: %w", id, err)
	}

	return channelFromMongo(doc), nil
}

func (p *mongoProvider) ListChannels(ctx context.Context) ([]Channel, error) {
	cur, err := p.coll.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("channel mongo: list: %w", err)
	}
	defer cur.Close(ctx)

	var docs []mongoChannel
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("channel mongo: decode: %w", err)
	}

	out := make([]Channel, len(docs))
	for i, d := range docs {
		out[i] = channelFromMongo(d)
	}

	return out, nil
}

func (p *mongoProvider) Close() error { return nil }

func channelFromMongo(d mongoChannel) Channel {
	return Channel(d)
}

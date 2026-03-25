package schedule

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoProvider implements ScheduleProvider using a MongoDB collection.
//
// Expected document shape:
//
//	{
//	  "_id":              "sched-1",
//	  "channel_id":       "cam-1",
//	  "storage_path":     "/data/recordings",
//	  "segment_minutes":  5,
//	  "start_at":         ISODate("2026-01-01T00:00:00Z"),  // optional
//	  "end_at":           ISODate("2026-12-31T23:59:59Z"),  // optional
//	  "days_of_week":     [1, 2, 3, 4, 5]                  // optional; 0=Sun
//	}
type mongoScheduleProvider struct {
	coll *mongo.Collection
}

// NewMongoProvider returns a ScheduleProvider backed by the given MongoDB collection.
// The caller owns the client and is responsible for disconnecting it.
func NewMongoProvider(coll *mongo.Collection) ScheduleProvider {
	return &mongoScheduleProvider{coll: coll}
}

// mongoSchedule is the BSON document representation of a Schedule.
type mongoSchedule struct {
	ID             string     `bson:"_id"`
	ChannelID      string     `bson:"channel_id"`
	StoragePath    string     `bson:"storage_path"`
	SegmentMinutes int        `bson:"segment_minutes"`
	StartAt        *time.Time `bson:"start_at"`
	EndAt          *time.Time `bson:"end_at"`
	DaysOfWeek     []int      `bson:"days_of_week"`
}

func (p *mongoScheduleProvider) ListSchedules(ctx context.Context) ([]Schedule, error) {
	cur, err := p.coll.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("schedule mongo: list: %w", err)
	}
	defer cur.Close(ctx)

	var docs []mongoSchedule
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("schedule mongo: decode: %w", err)
	}

	out := make([]Schedule, len(docs))
	for i, d := range docs {
		out[i] = scheduleFromMongo(d)
	}

	return out, nil
}

func (p *mongoScheduleProvider) Close() error { return nil }

func scheduleFromMongo(d mongoSchedule) Schedule {
	s := Schedule{
		ID:             d.ID,
		ChannelID:      d.ChannelID,
		StoragePath:    d.StoragePath,
		SegmentMinutes: d.SegmentMinutes,
	}

	if d.StartAt != nil {
		s.StartAt = d.StartAt.UTC()
	}

	if d.EndAt != nil {
		s.EndAt = d.EndAt.UTC()
	}

	s.DaysOfWeek = make([]time.Weekday, len(d.DaysOfWeek))
	for i, day := range d.DaysOfWeek {
		s.DaysOfWeek[i] = time.Weekday(day)
	}

	return s
}

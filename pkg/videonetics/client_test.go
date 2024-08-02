package videonetics

import (
	"context"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	ctx := context.Background()
	type args struct {
		uri string
		ctx *context.Context
	}
	tests := []struct {
		name    string
		args    args
		want    *Conn
		wantErr bool
	}{
		{
			name: "dd",
			args: args{
				"ddd",
				&ctx,
			},
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewClient(tt.args.uri, tt.args.ctx)
			go got.ReadFramePVA()
			time.Sleep(30 * time.Second)
			// if !reflect.DeepEqual(got, tt.want) {
			// 	t.Errorf("NewClient() = %v, want %v", got, tt.want)
			// }
		})
	}
}

package videonetics

import (
	"reflect"
	"testing"
)

func TestParseVideoneticsUri(t *testing.T) {
	type args struct {
		uri string
	}
	tests := []struct {
		name        string
		args        args
		wantHost    string
		wantChannel Channel
		wantErr     bool
	}{
		{
			name: "test uri channel 1",
			args: args{
				uri: "videonetics://172.16.1.146:20003/1/1/0",
			},
			wantHost: "dns:///172.16.1.146:20003",
			wantChannel: Channel{
				SiteID:    1,
				ChannelID: 1,
				AppID:     0,
				LiveOrRec: 1,
			},
			wantErr: false,
		},
		{
			name: "test uri channel 3",
			args: args{
				uri: "videonetics://172.16.1.146:20003/1/3/0",
			},
			wantHost: "dns:///172.16.1.146:20003",
			wantChannel: Channel{
				SiteID:    1,
				ChannelID: 3,
				AppID:     0,
				LiveOrRec: 1,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHost, gotChannel, err := ParseVideoneticsUri(tt.args.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseVideoneticsUri() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotHost != tt.wantHost {
				t.Errorf("ParseVideoneticsUri() gotHost = %v, want %v", gotHost, tt.wantHost)
			}
			if !reflect.DeepEqual(gotChannel, tt.wantChannel) {
				t.Errorf("ParseVideoneticsUri() gotChannel = %v, want %v", gotChannel, tt.wantChannel)
			}
		})
	}
}

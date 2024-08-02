package core

import "testing"

func TestTimeStamp90000(t *testing.T) {
	type args struct {
		timeStamp int64
	}
	tests := []struct {
		name string
		args args
		want uint32
	}{
		{
			name: "test epochtime",
			args: args{
				timeStamp: 1722327650000,
			},
			want: 1498048618,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TimeStamp90000(tt.args.timeStamp); got != tt.want {
				t.Errorf("TimeStamp90000() = %v, want %v", got, tt.want)
			}
		})
	}
}

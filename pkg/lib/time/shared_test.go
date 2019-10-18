package time

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSharedTime(t *testing.T) {
	now := time.Now()
	type fields struct {
		RWMutex sync.RWMutex
		time    time.Time
	}
	type args struct {
		current time.Time
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "TestSet",
			fields: fields{
				RWMutex: sync.RWMutex{},
				time:    time.Unix(12345567, 0),
			},
			args: args{current: now},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SharedTime{
				RWMutex: tt.fields.RWMutex,
				time:    tt.fields.time,
			}
			s.Set(now)
			require.True(t, s.Before(now.Add(time.Second)))
			require.True(t, s.After(now.Add(-time.Second)))
		})
	}
}

package time

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSharedTime(t *testing.T) {
	now := time.Now()
	type args struct {
		current time.Time
	}
	tests := []struct {
		name string
		s    *SharedTime
		args args
	}{
		{
			name: "TestSet",
			s: &SharedTime{
				RWMutex: sync.RWMutex{},
				time:    time.Unix(12345567, 0),
			},
			args: args{current: now},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.Set(now)
			require.True(t, tt.s.Before(now.Add(time.Second)))
			require.True(t, tt.s.After(now.Add(-time.Second)))
		})
	}
}

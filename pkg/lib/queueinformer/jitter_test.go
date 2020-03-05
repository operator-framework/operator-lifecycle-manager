package queueinformer

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResyncWithJitter(t *testing.T) {
	type args struct {
		resyncPeriod time.Duration
		factor       float64
	}
	tests := []struct {
		name    string
		args    args
		wantMin time.Duration
		wantMax time.Duration
	}{
		{
			name: "TypicalInput/Minutes",
			args: args{
				resyncPeriod: 15 * time.Minute,
				factor:       0.2,
			},
			wantMin: 12 * time.Minute,
			wantMax: 18 * time.Minute,
		},
		{
			name: "TypicalInput/Hours",
			args: args{
				resyncPeriod: 10 * time.Hour,
				factor:       0.1,
			},
			wantMin: 9 * time.Hour,
			wantMax: 11 * time.Hour,
		},
		{
			name: "BadInput/BadFactor",
			args: args{
				resyncPeriod: 10 * time.Hour,
				factor:       -0.1,
			},
			wantMin: 10 * time.Hour,
			wantMax: 10 * time.Hour,
		},
		{
			name: "BadInput/BadResync",
			args: args{
				resyncPeriod: -10 * time.Hour,
				factor:       0.1,
			},
			wantMin: DefaultResyncPeriod,
			wantMax: DefaultResyncPeriod,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResyncWithJitter(tt.args.resyncPeriod, tt.args.factor)
			require.True(t, got() >= tt.wantMin)
			require.True(t, got() <= tt.wantMax)
			require.True(t, got() != got() || tt.wantMax == tt.wantMin)
		})
	}
}

type float01 float64

func (float01) Generate(rand *rand.Rand, size int) reflect.Value {
	return reflect.ValueOf(float01(rand.Float64()))
}

type dur time.Duration

func (dur) Generate(rand *rand.Rand, size int) reflect.Value {
	return reflect.ValueOf(dur(rand.Uint64() / 2))
}

func TestGeneratesWithinRange(t *testing.T) {
	f := func(resync dur, factor float01) bool {
		resyncPeriod := time.Duration(resync)
		min := float64(resyncPeriod.Nanoseconds()) * (1 - float64(factor))
		max := float64(resyncPeriod.Nanoseconds()) * (1 + float64(factor))
		d := ResyncWithJitter(resyncPeriod, float64(factor))()
		return min < float64(d.Nanoseconds()) && float64(d.Nanoseconds()) < max
	}
	require.NoError(t, quick.Check(f, nil))
}

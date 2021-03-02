package queueinformer

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeFloat64er float64

func (f fakeFloat64er) Float64() float64 {
	return float64(f)
}

func TestResyncWithJitter(t *testing.T) {
	type args struct {
		resyncPeriod time.Duration
		factor       float64
		r            float64
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "TypicalInput/15Minutes/Min",
			args: args{
				resyncPeriod: 15 * time.Minute,
				factor:       0.2,
				r:            0,
			},
			want: 12 * time.Minute,
		},
		{
			name: "TypicalInput/15Minutes/Mid",
			args: args{
				resyncPeriod: 15 * time.Minute,
				factor:       0.2,
				r:            0.5,
			},
			want: 15 * time.Minute,
		},
		{
			name: "TypicalInput/15Minutes/Max",
			args: args{
				resyncPeriod: 15 * time.Minute,
				factor:       0.2,
				r:            1,
			},
			want: 18 * time.Minute,
		},
		{
			name: "TypicalInput/10Hours/Min",
			args: args{
				resyncPeriod: 10 * time.Hour,
				factor:       0.1,
				r:            0,
			},
			want: 9 * time.Hour,
		},
		{
			name: "TypicalInput/10Hours/Mid",
			args: args{
				resyncPeriod: 10 * time.Hour,
				factor:       0.1,
				r:            0.5,
			},
			want: 10 * time.Hour,
		},
		{
			name: "TypicalInput/10Hours/Max",
			args: args{
				resyncPeriod: 10 * time.Hour,
				factor:       0.1,
				r:            1,
			},
			want: 11 * time.Hour,
		},
		{
			name: "BadInput/BadFactor",
			args: args{
				resyncPeriod: 10 * time.Hour,
				factor:       -0.1,
			},
			want: 10 * time.Hour,
		},
		{
			name: "BadInput/BadResync",
			args: args{
				resyncPeriod: -10 * time.Hour,
				factor:       0.1,
			},
			want: DefaultResyncPeriod,
		},
		{
			name: "BadInput/Big",
			args: args{
				resyncPeriod: time.Duration(math.MaxInt64),
				factor:       1,
				r:            1,
			},
			want: time.Duration(math.MaxInt64),
		},
		{
			name: "SmallInput/Min",
			args: args{
				resyncPeriod: 10 * time.Second,
				factor:       0.5,
				r:            0,
			},
			want: 5 * time.Second,
		},
		{
			name: "SmallInput/Mid",
			args: args{
				resyncPeriod: 10 * time.Second,
				factor:       0.5,
				r:            0.5,
			},
			want: 10 * time.Second,
		},
		{
			name: "SmallInput/Max",
			args: args{
				resyncPeriod: 10 * time.Second,
				factor:       0.5,
				r:            1,
			},
			want: 15 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resyncWithJitter(tt.args.resyncPeriod, tt.args.factor, fakeFloat64er(tt.args.r))()
			require.Equal(t, tt.want, got)
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

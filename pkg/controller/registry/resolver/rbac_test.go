package resolver

import (
	"github.com/stretchr/testify/require"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

func TestGenerateName(t *testing.T) {
	type args struct {
		base string
		o    interface{}
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "generate",
			args: args{
				base: "myname",
				o: []string{"something"},
			},
			want: "myname-9c895f74f",
		},
		{
			name: "truncated",
			args: args{
				base: strings.Repeat("name", 100),
				o: []string{"something", "else"},
			},
			want: "namenamenamenamenamenamenamenamenamenamenamenamename-78fd8b4d6b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateName(tt.args.base, tt.args.o)
			require.Equal(t, tt.want, got)
			require.LessOrEqual(t, len(got), maxNameLength)
		})
	}
}

var runeSet = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-")

type validKubeName string

func (validKubeName) Generate(rand *rand.Rand, size int) reflect.Value {
	b := make([]rune, size)
	for i := range b {
		b[i] = runeSet[rand.Intn(len(runeSet))]
	}
	return reflect.ValueOf(validKubeName(b))
}

func TestGeneratesWithinRange(t *testing.T) {
	f := func(base validKubeName, o string) bool {
		return len(generateName(string(base), o)) <= maxNameLength
	}
	require.NoError(t, quick.Check(f, nil))
}

package version

import (
	"encoding/json"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
)

func TestOperatorVersionMarshal(t *testing.T) {
	tests := []struct {
		name string
		in   OperatorVersion
		out  []byte
		err  error
	}{
		{
			name: "MMP",
			in:   OperatorVersion{semver.MustParse("1.2.3")},
			out:  []byte(`"1.2.3"`),
		},
		{
			name: "empty",
			in:   OperatorVersion{semver.Version{}},
			out:  []byte(`"0.0.0"`),
		},
		{
			name: "with-timestamp",
			in:   OperatorVersion{semver.MustParse("1.2.3-1556715351")},
			out:  []byte(`"1.2.3-1556715351"`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := tt.in.MarshalJSON()
			require.Equal(t, tt.out, m, string(m))
			require.Equal(t, tt.err, err)
		})
	}
}

func TestOperatorVersionUnmarshal(t *testing.T) {
	type TestStruct struct {
		Version OperatorVersion `json:"v"`
	}
	tests := []struct {
		name string
		in   []byte
		out  TestStruct
		err  error
	}{
		{
			name: "MMP",
			in:   []byte(`{"v": "1.2.3"}`),
			out:  TestStruct{Version: OperatorVersion{semver.MustParse("1.2.3")}},
		},
		{
			name: "empty",
			in:   []byte(`{"v": "0.0.0"}`),
			out:  TestStruct{Version: OperatorVersion{semver.Version{Major: 0, Minor: 0, Patch: 0}}},
		},
		{
			name: "with-timestamp",
			in:   []byte(`{"v": "1.2.3-1556715351"}`),
			out:  TestStruct{OperatorVersion{semver.MustParse("1.2.3-1556715351")}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := TestStruct{}
			err := json.Unmarshal(tt.in, &s)
			require.Equal(t, tt.out, s)
			require.Equal(t, tt.err, err)
		})
	}
}

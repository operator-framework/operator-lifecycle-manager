package runtime_constraints

import (
	"fmt"
	"os"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/stretchr/testify/require"
)

func TestNew_HappyPath(t *testing.T) {
	predicates := []cache.Predicate{
		cache.PkgPredicate("etcd"),
		cache.LabelPredicate("something"),
	}
	provider, err := New(predicates)
	require.Nil(t, err)
	require.Equal(t, predicates, provider.Constraints())
}

func TestNew_TooManyConstraints(t *testing.T) {
	predicates := make([]cache.Predicate, MaxRuntimeConstraints+1)
	for i := 0; i < len(predicates); i++ {
		predicates[i] = cache.PkgPredicate(fmt.Sprintf("etcd-%d", i))
	}
	provider, err := New(predicates)
	require.NotNil(t, err)
	require.Nil(t, provider)
}

func TestNewFromFile_HappyPath(t *testing.T) {
	provider, err := NewFromFile("testdata/runtime_constraints.json")
	require.Nil(t, err)
	require.Len(t, provider.Constraints(), 1)
	require.Equal(t, provider.Constraints()[0], cache.PkgPredicate("etcd"))
}

func TestNewFromFile_ErrorOnNotFound(t *testing.T) {
	provider, err := NewFromFile("testdata/not/a/real/path.json")
	require.NotNil(t, err)
	require.Nil(t, provider)
}

func TestNewFromFile_TooManyConstraints(t *testing.T) {
	provider, err := NewFromFile("testdata/too_many_constraints.json")
	require.NotNil(t, err)
	require.Nil(t, provider)
}

func TestNewFromEnv_HappyPath(t *testing.T) {
	require.Nil(t, os.Setenv(RuntimeConstraintEnvVarName, "testdata/runtime_constraints.json"))
	t.Cleanup(func() { _ = os.Unsetenv(RuntimeConstraintEnvVarName) })

	provider, err := NewFromEnv()
	require.Nil(t, err)
	require.Len(t, provider.Constraints(), 1)
	require.Equal(t, provider.Constraints()[0], cache.PkgPredicate("etcd"))
}

func TestNewFromEnv_ErrorOnNotFound(t *testing.T) {
	testCases := []struct {
		title string
		value string
	}{
		{
			title: "File not found",
			value: "testdata/not/a/real/path.json",
		}, {
			title: "Bad path",
			value: "fsdkljflsdk ropweiropw 4434!@#!#",
		}, {
			title: "No env var set",
			value: "nil",
		},
	}

	for _, testCase := range testCases {
		if testCase.value != "nil" {
			require.Nil(t, os.Setenv(RuntimeConstraintEnvVarName, testCase.value))
			t.Cleanup(func() { _ = os.Unsetenv(RuntimeConstraintEnvVarName) })
		}
		provider, err := NewFromEnv()
		require.NotNil(t, err)
		require.Nil(t, provider)
	}
}

func TestNewFromEnv_TooManyConstraints(t *testing.T) {
	require.Nil(t, os.Setenv(RuntimeConstraintEnvVarName, "testdata/too_many_constraints.json"))
	t.Cleanup(func() { _ = os.Unsetenv(RuntimeConstraintEnvVarName) })
	provider, err := NewFromEnv()
	require.NotNil(t, err)
	require.Nil(t, provider)
}

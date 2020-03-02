package queueinformer

import (
	"context"
	"testing"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/version"
)

type versionFunc func() (*version.Info, error)

func (f versionFunc) ServerVersion() (*version.Info, error) {
	if f == nil {
		return &version.Info{}, nil
	}
	return (func() (*version.Info, error))(f)()
}

func TestOperatorRunChannelClosure(t *testing.T) {
	for _, tc := range []struct {
		name string
		// set up the operator under test and return a cleanup func to be invoked when the test completes
		of func(cancel context.CancelFunc, o *operator) func()
	}{
		{
			name: "error getting server version",
			of: func(cancel context.CancelFunc, o *operator) func() {
				o.serverVersion = versionFunc(func() (*version.Info, error) {
					return nil, errors.New("test error")
				})
				return func() {}
			},
		},
		{
			name: "context cancelled while getting server version",
			of: func(cancel context.CancelFunc, o *operator) func() {
				done := make(chan struct{})
				o.serverVersion = versionFunc(func() (*version.Info, error) {
					defer func() {
						<-done
					}()
					cancel()
					return nil, errors.New("test error")
				})
				return func() {
					close(done)
				}
			},
		},
		{
			name: "context cancelled before cache sync",
			of: func(cancel context.CancelFunc, o *operator) func() {
				o.hasSynced = func() bool {
					cancel()
					return false
				}
				return func() {}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o, err := newOperatorFromConfig(defaultOperatorConfig())
			if err != nil {
				t.Fatalf("could not create operator from default config: %s", err)
			}
			o.serverVersion = versionFunc(nil)
			o.hasSynced = func() bool { return true }

			done := func() {}
			if tc.of != nil {
				done = tc.of(cancel, o)
			}
			defer done()

			o.Run(ctx)

			timeout := time.After(time.Second)
			for n, ch := range map[string]<-chan struct{}{
				"ready": o.Ready(),
				"done":  o.Done(),
			} {
				select {
				case <-ch:
				case <-timeout:
					t.Errorf("timed out before %s channel closed", n)
				}
			}
		})
	}
}

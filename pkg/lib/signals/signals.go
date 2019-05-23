package signals

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	shutdownSignals      = []os.Signal{os.Interrupt, syscall.SIGTERM}
	onlyOneSignalHandler = make(chan struct{})
)

// SetupSignalHandler registered for SIGTERM and SIGINT. A stop channel is returned
// which is closed on one of these signals. If a second signal is caught, the program
// is terminated with exit code 1.
func SetupSignalHandler() (stopCh <-chan struct{}) {
	close(onlyOneSignalHandler) // panics when called twice

	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		close(stop)
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return stop
}

var (
	signalCtx context.Context
	cancel    context.CancelFunc
	once      sync.Once
)

// Context returns a Context registered to close on SIGTERM and SIGINT.
// If a second signal is caught, the program is terminated with exit code 1.
func Context() context.Context {
	once.Do(func() {
		c := make(chan os.Signal, 2)
		signal.Notify(c, shutdownSignals...)
		signalCtx, cancel = context.WithCancel(context.Background())
		go func() {
			<-c
			cancel()

			select {
			case <-signalCtx.Done():
			case <-c:
				os.Exit(1) // second signal. Exit directly.
			}
		}()
	})

	return signalCtx
}

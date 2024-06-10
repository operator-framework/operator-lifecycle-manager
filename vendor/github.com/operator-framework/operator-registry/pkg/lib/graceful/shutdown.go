package graceful

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

func Shutdown(logger logrus.FieldLogger, run func() error, cleanup func()) error {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)

	g, ctx := errgroup.WithContext(context.Background())
	g.Go(run)

	select {
	case <-interrupt:
		break
	case <-ctx.Done():
		break
	}

	logger.Info("shutting down...")

	cleanup()

	return g.Wait()
}

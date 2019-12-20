package filemonitor

import (
	"context"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type watcher struct {
	notify       *fsnotify.Watcher
	pathsToWatch []string
	logger       *logrus.Logger
	onUpdateFn   func(*logrus.Logger, fsnotify.Event)
}

// NewWatch sets up monitoring on a slice of paths and will execute the update function to process each event
func NewWatch(logger *logrus.Logger, pathsToWatch []string, onUpdateFn func(*logrus.Logger, fsnotify.Event)) (*watcher, error) {
	notify, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	for _, item := range pathsToWatch {
		// (non-recursive if a directory is added)
		if err := notify.Add(item); err != nil {
			return nil, err
		}
		logger.Debugf("monitoring path '%v'", item)
	}

	newWatcher := &watcher{
		notify:       notify,
		pathsToWatch: pathsToWatch,
		onUpdateFn:   onUpdateFn,
		logger:       logger,
	}

	return newWatcher, nil
}

func (w *watcher) Run(ctx context.Context) {
	go func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				w.notify.Close() // always returns nil for the error
				w.logger.Debug("terminating watcher")
				return
			case event := <-w.notify.Events:
				w.logger.Debugf("watcher got event: %v", event)
				if w.onUpdateFn != nil {
					w.onUpdateFn(w.logger, event)
				}
			case err := <-w.notify.Errors:
				w.logger.Warnf("watcher got error: %v", err)
			}
		}
	}(ctx)
}

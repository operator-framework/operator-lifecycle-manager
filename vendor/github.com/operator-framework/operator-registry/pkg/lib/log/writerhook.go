package log

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

// Taken from https://github.com/sirupsen/logrus/issues/678
// Used to split log level output until this is implemented internally by logrus

// WriterHook is a hook that writes logs of specified LogLevels to specified Writer
type WriterHook struct {
	Writer    io.Writer
	LogLevels []logrus.Level
}

// Fire will be called when some logging function is called with current hook
// It will format log entry to string and write it to appropriate writer
func (hook *WriterHook) Fire(entry *logrus.Entry) error {
	line, err := entry.String()
	if err != nil {
		return err
	}
	_, err = hook.Writer.Write([]byte(line))
	return err
}

// Levels define on which log levels this hook would trigger
func (hook *WriterHook) Levels() []logrus.Level {
	return hook.LogLevels
}

// AddHooks adds hooks to send logs to different destinations depending on level
func AddHooks(hooks ...*WriterHook) {
	// Send all logs to nowhere by default
	logrus.SetOutput(io.Discard)

	for _, hook := range hooks {
		logrus.AddHook(hook)
	}
}

func AddDefaultWriterHooks(terminationLogPath string) error {
	terminationLogFile, err := os.OpenFile(terminationLogPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	AddHooks(
		&WriterHook{
			Writer: terminationLogFile,
			LogLevels: []logrus.Level{
				logrus.PanicLevel,
				logrus.FatalLevel,
			},
		},
		&WriterHook{
			Writer: os.Stderr,
			LogLevels: []logrus.Level{
				logrus.PanicLevel,
				logrus.FatalLevel,
				logrus.ErrorLevel,
				logrus.WarnLevel,
			},
		},
		&WriterHook{
			Writer: os.Stdout,
			LogLevels: []logrus.Level{
				logrus.InfoLevel,
				logrus.DebugLevel,
			},
		})

	return nil
}

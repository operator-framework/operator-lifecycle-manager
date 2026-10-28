package log

import (
	"io"

	"github.com/sirupsen/logrus"
)

func Null() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return logrus.NewEntry(l)
}

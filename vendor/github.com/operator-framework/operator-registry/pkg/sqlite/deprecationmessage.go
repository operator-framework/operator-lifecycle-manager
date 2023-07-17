package sqlite

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

const noticeColor = "\033[1;33m%s\033[0m"

func LogSqliteDeprecation() {
	log := logrus.New()
	log.Warnf(DeprecationMessage)
}

var DeprecationMessage = fmt.Sprintf(noticeColor, `DEPRECATION NOTICE:
Sqlite-based catalogs and their related subcommands are deprecated. Support for
them will be removed in a future release. Please migrate your catalog workflows
to the new file-based catalog format.`)

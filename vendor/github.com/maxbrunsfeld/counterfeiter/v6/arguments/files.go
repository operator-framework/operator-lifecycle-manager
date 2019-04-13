package arguments

import "os"

type SymlinkEvaler func(string) (string, error)
type FileStatReader func(string) (os.FileInfo, error)

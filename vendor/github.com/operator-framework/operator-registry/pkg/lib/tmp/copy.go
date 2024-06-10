package tmp

import (
	"fmt"
	"io"
	"os"
)

// CopyTmpDB reads the file at the given path and copies it to a tmp directory, returning the copied file path or an err
func CopyTmpDB(original string) (path string, err error) {
	dst, err := os.CreateTemp("", "db-")
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := dst.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	src, err := OpenRegularFile(original)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := src.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(dst, src)
	if err != nil {
		return "", err
	}

	return dst.Name(), nil
}

// OpenRegularFile opens the file at path and returns an error if it is not regular, does not exist, or cannot be opened
func OpenRegularFile(path string) (*os.File, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := fd.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return fd, nil
}

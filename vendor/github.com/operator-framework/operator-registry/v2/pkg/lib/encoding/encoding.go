package encoding

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
)

// GzipBase64Encode applies gzip compression to the given bytes, followed by base64 encoding.
func GzipBase64Encode(data []byte) ([]byte, error) {
	buf := &bytes.Buffer{}

	bWriter := base64.NewEncoder(base64.StdEncoding, buf)
	zWriter := gzip.NewWriter(bWriter)
	_, err := zWriter.Write(data)
	if err != nil {
		zWriter.Close()
		bWriter.Close()
		return nil, err
	}

	// Ensure all gzipped bytes are flushed to the underlying base64 encoder
	err = zWriter.Close()
	if err != nil {
		return nil, err
	}

	// Ensure all base64d bytes are flushed to the underlying buffer
	err = bWriter.Close()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// GzipBase64Decode applies base64 decoding to the given bytes, followed by gzip decompression.
func GzipBase64Decode(data []byte) ([]byte, error) {
	bBuffer := bytes.NewReader(data)

	bReader := base64.NewDecoder(base64.StdEncoding, bBuffer)
	zReader, err := gzip.NewReader(bReader)
	if err != nil {
		return nil, err
	}
	defer zReader.Close()

	return io.ReadAll(zReader)
}

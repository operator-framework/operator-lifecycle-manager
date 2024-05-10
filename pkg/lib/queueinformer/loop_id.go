package queueinformer

import (
	"crypto/rand"
	"encoding/base64"
)

func NewLoopID() string {
	const buffSize = 5
	buff := make([]byte, buffSize)
	rand.Read(buff)
	str := base64.StdEncoding.EncodeToString(buff)
	return str[:buffSize]
}

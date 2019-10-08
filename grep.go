package asdf

import (
	"bytes"
	"io"
)

const bufferSize = 1 << 16

func Grep(reader io.Reader, needle []byte) (int, error) {
	tailSize := len(needle) - 1
	buffer := make([]byte, bufferSize + tailSize)
	pos := -1
	var n, offset int
	var err error
	for err == nil && pos < 0 {
		offset += n
		n, err = io.ReadFull(reader, buffer[tailSize:])
		pos = bytes.Index(buffer, needle)
		copy(buffer, buffer[n:])
	}
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return -1, err
	}
	if pos < 0 {
		return -1, nil
	}
	return offset + pos - tailSize, nil
}

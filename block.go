package asdf

import (
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"io"
	"io/ioutil"

	"github.com/pierrec/lz4"
	"github.com/pkg/errors"
)

var blockMagic = [4]byte{0xd3, 0x42, 0x4c, 0x4b}

type CompressionKind int

const (
	CompressionNone  CompressionKind = iota
	CompressionZLIB  CompressionKind = iota
	CompressionBZIP2 CompressionKind = iota
	CompressionLZ4   CompressionKind = iota

	FlagStreamed uint32 = 1
)

type Block struct {
	Data []byte
	Flags uint32
	Compression CompressionKind

	dataSize uint64
	checksum []byte
}

var compressionMapping = map[string]CompressionKind {
	"\x00\x00\x00\x00": CompressionNone,
	"zlib": CompressionZLIB,
	"bzp2": CompressionBZIP2,
	"lz4\x00": CompressionLZ4,
}

var compressionNames = map[CompressionKind]string {
	CompressionNone: "none",
	CompressionZLIB: "zlib",
	CompressionBZIP2: "bzip2",
	CompressionLZ4: "lz4",
}

var decompressors = map[CompressionKind]func(reader io.Reader) (io.Reader, error) {
	CompressionNone: newNoneReader,
	CompressionZLIB: newZlibReader,
	CompressionBZIP2: newBzip2Reader,
	CompressionLZ4: newLZ4Reader,
}

func (block *Block) Uncompress() error {
	reader, err := decompressors[block.Compression](bytes.NewBuffer(block.Data))
	if err != nil {
		return errors.Wrapf(err, "failed to decompress %d bytes with %s",
			len(block.Data), compressionNames[block.Compression])
	}
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return errors.Wrapf(err, "failed to decompress %d bytes with %s",
			len(block.Data), compressionNames[block.Compression])
	}
	block.Data = data
	block.Compression = CompressionNone
	if !bytes.Equal(block.checksum, bytes.Repeat([]byte{0}, 16)) {
		// check the checksum
		hash := md5.New()
		hash.Write(block.Data)
		if !bytes.Equal(hash.Sum(nil), block.checksum) {
			return errors.Errorf("block checksum mismatch: actual %v vs declared %v",
				hash.Sum(nil), block.checksum)
		}
	}
	return nil
}

func ReadBlock(reader io.Reader) (*Block, error) {
	block := &Block{}
	buffer := make([]byte, 4)
	_, err := io.ReadFull(reader, buffer)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read the block's magic")
	}
	if !bytes.Equal(buffer, blockMagic[:]) {
		return nil, errors.Errorf("block magic does not match: %v", buffer)
	}
	buffer = buffer[:2]
	_, err = io.ReadFull(reader, buffer)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read the block's header size")
	}
	headerSize := binary.BigEndian.Uint16(buffer)
	buffer = make([]byte, headerSize)
	_, err = io.ReadFull(reader, buffer)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read the block's header")
	}
	offset := 0
	block.Flags = binary.BigEndian.Uint32(buffer[:4])
	offset += 4
	compression := buffer[offset:offset+4]
	offset += 4
	var exists bool
	block.Compression, exists = compressionMapping[string(compression)]
	if !exists {
		return nil, errors.Errorf("unsupported block compression: %s", string(compression))
	}
	allocatedSize := binary.BigEndian.Uint64(buffer[offset:offset+8])
	offset += 8
	usedSize := binary.BigEndian.Uint64(buffer[offset:offset+8])
	offset += 8
	block.dataSize = binary.BigEndian.Uint64(buffer[offset:offset+8])
	offset += 8
	block.checksum = buffer[offset:offset+16]
	block.Data = make([]byte, usedSize)
	_, err = io.ReadFull(reader, block.Data)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read the block's payload")
	}
	sink := make([]byte, allocatedSize-usedSize)
	_, err = io.ReadFull(reader, sink)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read the block's remainder")
	}
	return block, nil
}

func newNoneReader(reader io.Reader) (io.Reader, error) {
	return reader, nil
}

func newZlibReader(reader io.Reader) (io.Reader, error) {
	return zlib.NewReader(reader)
}

func newBzip2Reader(reader io.Reader) (io.Reader, error) {
	return bzip2.NewReader(reader), nil
}

func newLZ4Reader(reader io.Reader) (io.Reader, error) {
	// The underlying format is LZ4 block.
	//  4 bytes   +    4 bytes      + data
	// block size  uncompressed size
	writer := &bytes.Buffer{}
	sizeBuffer := make([]byte, 4)
	for {
		_, err := io.ReadFull(reader, sizeBuffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		size := binary.BigEndian.Uint32(sizeBuffer)
		lz4data := make([]byte, size - 4)
		_, err = io.ReadFull(reader, sizeBuffer)
		if err != nil {
			return nil, err
		}
		size = binary.LittleEndian.Uint32(sizeBuffer)
		dest := make([]byte, size)
		_, err = io.ReadFull(reader, lz4data)
		if err != nil {
			return nil, err
		}
		n, err := lz4.UncompressBlock(lz4data, dest)
		if err != nil {
			return nil, errors.Wrap(err, "lz4 error")
		}
		if n != len(dest) {
			return nil, errors.Errorf("uncompressed LZ4 size mismatch: %d != %d", n, size)
		}
		writer.Write(dest)
	}
	return bytes.NewReader(writer.Bytes()), nil
}

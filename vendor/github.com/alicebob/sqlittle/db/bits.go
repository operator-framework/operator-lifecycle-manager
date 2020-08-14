package db

// Same as encoding.binary.ReadVarInt, but that one is little endian.
// Returns: the number, bytes needed.
// Will return (0, -1) if there are not enough bytes available.
func readVarint(b []byte) (int64, int) {
	var n uint64
	for i := 0; ; i++ {
		if i >= len(b) {
			// oops
			return 0, -1
		}
		c := b[i]
		if i == 8 {
			n = (n << 8) | uint64(c)
			return int64(n), i + 1
		}
		n = (n << 7) | uint64(c&0x7f)
		if c < 0x80 {
			return int64(n), i + 1
		}
	}
}

// Read a 24 bits two-complement integer.
// b needs to be at least 3 bytes long
func readTwos24(b []byte) int64 {
	n := int64(
		uint64(b[0])<<16 |
			uint64(b[1])<<8 |
			uint64(b[2]))
	if n&(1<<23) != 0 {
		n -= (1 << 24)
	}
	return n
}

// Read a 48 bits two-complement integer.
// b needs to be at least 6 bytes long
func readTwos48(b []byte) int64 {
	n := int64(
		uint64(b[0])<<40 |
			uint64(b[1])<<32 |
			uint64(b[2])<<24 |
			uint64(b[3])<<16 |
			uint64(b[4])<<8 |
			uint64(b[5]))
	if n&(1<<47) != 0 {
		n -= (1 << 48)
	}
	return n
}

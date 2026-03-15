package schema

import (
	"crypto/rand"
	"fmt"
	"time"
)

// NewEventID generates a UUID v7 string with millisecond-precision timestamp prefix
// for time-sortable, globally unique event identification.
func NewEventID() string {
	var uuid [16]byte

	ms := uint64(time.Now().UnixMilli())
	uuid[0] = byte(ms >> 40)
	uuid[1] = byte(ms >> 32)
	uuid[2] = byte(ms >> 24)
	uuid[3] = byte(ms >> 16)
	uuid[4] = byte(ms >> 8)
	uuid[5] = byte(ms)

	if _, err := rand.Read(uuid[6:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}

	uuid[6] = (uuid[6] & 0x0f) | 0x70
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4],
		uuid[4:6],
		uuid[6:8],
		uuid[8:10],
		uuid[10:16],
	)
}

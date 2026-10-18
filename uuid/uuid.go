// Package uuid implements RFC 9562 UUIDs (versions 4 and 7) without external
// dependencies. UUID implements sql.Scanner, driver.Valuer and the text/JSON
// marshaling interfaces.
package uuid

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// UUID is a 128 bit universally unique identifier.
type UUID [16]byte

// Nil is the zero UUID.
var Nil UUID

// ErrInvalid is returned for malformed input.
var ErrInvalid = errors.New("uuid: invalid format")

// NewV4 returns a random (version 4) UUID.
func NewV4() UUID {
	var u UUID
	_, _ = rand.Read(u[:])
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return u
}

var v7 struct {
	sync.Mutex
	lastMs int64
	seq    uint16
}

// now is replaceable in tests.
var now = time.Now

// NewV7 returns a time-ordered (version 7) UUID. UUIDs created by the same
// process are strictly increasing, even within the same millisecond.
func NewV7() UUID {
	var u UUID
	_, _ = rand.Read(u[:])

	v7.Lock()
	ms := now().UnixMilli()
	if ms <= v7.lastMs {
		v7.seq++
		if v7.seq > 0x0fff { // counter overflow: borrow the next millisecond
			v7.lastMs++
			v7.seq = 0
		}
		ms = v7.lastMs
	} else {
		v7.lastMs = ms
		v7.seq = uint16(u[6])<<8 | uint16(u[7])
		v7.seq &= 0x07ff // leave head room for increments
	}
	seq := v7.seq
	v7.Unlock()

	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)
	u[6] = 0x70 | byte(seq>>8)&0x0f
	u[7] = byte(seq)
	u[8] = u[8]&0x3f | 0x80
	return u
}

// Parse parses the canonical 36 character form (also accepts 32 hex chars and
// surrounding braces or the "urn:uuid:" prefix).
func Parse(s string) (UUID, error) {
	var u UUID
	switch {
	case len(s) == 45 && s[:9] == "urn:uuid:":
		s = s[9:]
	case len(s) == 38 && s[0] == '{' && s[37] == '}':
		s = s[1:37]
	}
	switch len(s) {
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return u, ErrInvalid
		}
		s = s[:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	case 32:
	default:
		return u, ErrInvalid
	}
	if _, err := hex.Decode(u[:], []byte(s)); err != nil {
		return Nil, ErrInvalid
	}
	return u, nil
}

// MustParse is like Parse but panics on error.
func MustParse(s string) UUID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// FromBytes returns the UUID stored in b (16 bytes, big endian).
func FromBytes(b []byte) (UUID, error) {
	var u UUID
	if len(b) != 16 {
		return u, ErrInvalid
	}
	copy(u[:], b)
	return u, nil
}

// String returns the canonical lower-case form.
func (u UUID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], u[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], u[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], u[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], u[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:], u[10:])
	return string(buf[:])
}

// Version returns the version number (0-15).
func (u UUID) Version() int { return int(u[6] >> 4) }

// IsZero reports whether u is the Nil UUID.
func (u UUID) IsZero() bool { return u == Nil }

// Time returns the embedded timestamp of a version 7 UUID.
func (u UUID) Time() (time.Time, bool) {
	if u.Version() != 7 {
		return time.Time{}, false
	}
	ms := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 | int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	return time.UnixMilli(ms), true
}

// Value implements driver.Valuer; UUIDs are sent as strings.
func (u UUID) Value() (driver.Value, error) { return u.String(), nil }

// Scan implements sql.Scanner. It accepts strings, textual byte slices and
// 16 byte binary values.
func (u *UUID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*u = Nil
		return nil
	case string:
		p, err := Parse(v)
		if err != nil {
			return err
		}
		*u = p
		return nil
	case []byte:
		if len(v) == 16 {
			copy(u[:], v)
			return nil
		}
		return u.Scan(string(v))
	case [16]byte:
		*u = v
		return nil
	}
	return fmt.Errorf("uuid: cannot scan %T", src)
}

// MarshalText implements encoding.TextMarshaler.
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (u *UUID) UnmarshalText(b []byte) error {
	p, err := Parse(string(b))
	if err != nil {
		return err
	}
	*u = p
	return nil
}

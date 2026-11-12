// Package lnk reads the target path out of a Windows shortcut (.lnk) file,
// following the published MS-SHLLINK layout. It is pure Go so it can be
// tested anywhere.
package lnk

import (
	"encoding/binary"
	"errors"
	"os"
	"unicode/utf16"
)

var errFormat = errors.New("not a shortcut with a local target")

// Target returns the local path a shortcut file points to.
func Target(file string) (string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if st.Size() > 1<<20 {
		return "", errFormat
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return Parse(data)
}

func Parse(d []byte) (string, error) {
	le := binary.LittleEndian
	if len(d) < 0x4C || le.Uint32(d) != 0x4C {
		return "", errFormat
	}
	flags := le.Uint32(d[0x14:])
	off := 0x4C
	if flags&0x1 != 0 { // HasLinkTargetIDList
		if off+2 > len(d) {
			return "", errFormat
		}
		off += 2 + int(le.Uint16(d[off:]))
	}
	if flags&0x2 == 0 || off+28 > len(d) { // HasLinkInfo
		return "", errFormat
	}
	li := d[off:]
	size := int(le.Uint32(li))
	if size < 28 || size > len(li) {
		return "", errFormat
	}
	li = li[:size]
	header := int(le.Uint32(li[4:]))
	if le.Uint32(li[8:])&0x1 == 0 { // VolumeIDAndLocalBasePath
		return "", errFormat
	}
	if header >= 0x24 && len(li) >= 0x24 {
		base := int(le.Uint32(li[28:]))
		suffix := int(le.Uint32(li[32:]))
		if base > 0 && base < len(li) {
			s := wide(li[base:])
			if suffix > 0 && suffix < len(li) {
				s += wide(li[suffix:])
			}
			if s != "" {
				return s, nil
			}
		}
	}
	base := int(le.Uint32(li[16:]))
	suffix := int(le.Uint32(li[24:]))
	if base <= 0 || base >= len(li) {
		return "", errFormat
	}
	s := narrow(li[base:])
	if suffix > 0 && suffix < len(li) {
		s += narrow(li[suffix:])
	}
	if s == "" {
		return "", errFormat
	}
	return s, nil
}

func wide(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// narrow reads a NUL-terminated string in the system code page. Only the
// ASCII range is certain; other bytes are kept as Latin-1 so the file name
// part, which is what callers need, usually survives.
func narrow(b []byte) string {
	var r []rune
	for _, c := range b {
		if c == 0 {
			break
		}
		r = append(r, rune(c))
	}
	return string(r)
}

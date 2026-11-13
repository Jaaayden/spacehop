package lnk

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// build assembles a minimal shortcut: header, optional ID list, LinkInfo.
func build(idList bool, ansi, uni string) []byte {
	le := binary.LittleEndian
	d := make([]byte, 0x4C)
	le.PutUint32(d, 0x4C)
	flags := uint32(0x2)
	if idList {
		flags |= 0x1
		d = append(d, 6, 0, 1, 2, 3, 4, 0, 0)
	}
	le.PutUint32(d[0x14:], flags)
	header := 0x1C
	if uni != "" {
		header = 0x24
	}
	li := make([]byte, header)
	le.PutUint32(li[4:], uint32(header))
	le.PutUint32(li[8:], 1)
	le.PutUint32(li[16:], uint32(len(li)))
	li = append(li, append([]byte(ansi), 0)...)
	le.PutUint32(li[24:], uint32(len(li)))
	li = append(li, 0)
	if uni != "" {
		le.PutUint32(li[28:], uint32(len(li)))
		for _, u := range utf16.Encode([]rune(uni)) {
			li = append(li, byte(u), byte(u>>8))
		}
		li = append(li, 0, 0)
		le.PutUint32(li[32:], uint32(len(li)))
		li = append(li, 0, 0)
	}
	le.PutUint32(li, uint32(len(li)))
	return append(d, li...)
}

func TestParse(t *testing.T) {
	cases := []struct {
		data []byte
		want string
	}{
		{build(false, `C:\Windows\notepad.exe`, ""), `C:\Windows\notepad.exe`},
		{build(true, `C:\Program Files\App\app.exe`, ""), `C:\Program Files\App\app.exe`},
		{build(true, `C:\??\wx.exe`, `C:\微信\wx.exe`), `C:\微信\wx.exe`},
	}
	for _, c := range cases {
		got, err := Parse(c.data)
		if err != nil || got != c.want {
			t.Fatalf("got %q, %v; want %q", got, err, c.want)
		}
	}
	for _, bad := range [][]byte{nil, make([]byte, 10), make([]byte, 0x4C), build(true, "x", "")[:0x60]} {
		if _, err := Parse(bad); err == nil {
			t.Fatal("malformed input must be rejected")
		}
	}
}

func TestParseLink(t *testing.T) {
	le := binary.LittleEndian
	d := build(true, `C:\App\app.exe`, "")
	str := func(s string) []byte {
		u := utf16.Encode([]rune(s))
		b := []byte{byte(len(u)), byte(len(u) >> 8)}
		for _, c := range u {
			b = append(b, byte(c), byte(c>>8))
		}
		return b
	}
	// name, working directory, arguments, all Unicode
	le.PutUint32(d[0x14:], le.Uint32(d[0x14:])|0x4|0x10|0x20|0x80)
	d = append(d, str("My app")...)
	d = append(d, str(`C:\App`)...)
	d = append(d, str(`--profile "工作" --new`)...)
	l, err := ParseLink(d)
	if err != nil || l.Target != `C:\App\app.exe` || l.Dir != `C:\App` || l.Args != `--profile "工作" --new` || l.Special {
		t.Fatalf("%+v %v", l, err)
	}
	le.PutUint32(d[0x14:], le.Uint32(d[0x14:])|0x2000) // run as administrator
	if l, _ := ParseLink(d); !l.Special {
		t.Fatal("an elevated shortcut must be left to the shell")
	}
	// Cut short anywhere: never panic, target still known.
	for n := len(d) - 1; n > len(d)-40; n-- {
		if l, err := ParseLink(d[:n]); err != nil || l.Target == "" {
			t.Fatalf("truncated at %d: %+v %v", n, l, err)
		}
	}
}

// Command mkres writes the resource objects the Go linker embeds into the
// Windows executable: the icon, the version information and the manifest.
// It replaces external resource compilers so the build needs only Go.
//
//	go run ./tools/mkres 1.2.3
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"spacehop/internal/icon"
)

var le = binary.LittleEndian

type resource struct {
	typ, id, lang uint32
	data          []byte
}

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="SpaceHop" version="%s" processorArchitecture="*"/>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3"><security><requestedPrivileges>
    <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
  </requestedPrivileges></security></trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1"><application>
    <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
  </application></compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings>
    <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
    <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true</dpiAware>
    <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
  </windowsSettings></application>
</assembly>`

func wstr(s string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, le, append(utf16.Encode([]rune(s)), 0))
	return b.Bytes()
}

func pad4(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

// node builds one block of a version resource: a header, a key, an
// optional value and child blocks, each aligned to four bytes.
func node(key string, text bool, value []byte, children ...[]byte) []byte {
	var b bytes.Buffer
	vlen := len(value)
	typ := uint16(0)
	if text {
		typ, vlen = 1, len(value)/2
	}
	_ = binary.Write(&b, le, [3]uint16{0, uint16(vlen), typ})
	b.Write(wstr(key))
	pad4(&b)
	b.Write(value)
	for _, c := range children {
		pad4(&b)
		b.Write(c)
	}
	out := b.Bytes()
	le.PutUint16(out, uint16(len(out)))
	return out
}

func versionInfo(v [4]uint16, text string) []byte {
	var fixed bytes.Buffer
	ms, ls := uint32(v[0])<<16|uint32(v[1]), uint32(v[2])<<16|uint32(v[3])
	_ = binary.Write(&fixed, le, [13]uint32{0xFEEF04BD, 0x10000, ms, ls, ms, ls, 0x3F, 0, 0x40004, 1, 0, 0, 0})
	var strs [][]byte
	for _, kv := range [][2]string{
		{"CompanyName", "SpaceHop"}, {"FileDescription", "SpaceHop"}, {"FileVersion", text}, {"InternalName", "SpaceHop"},
		{"OriginalFilename", "SpaceHop.exe"}, {"ProductName", "SpaceHop"}, {"ProductVersion", text},
	} {
		strs = append(strs, node(kv[0], true, wstr(kv[1])))
	}
	return node("VS_VERSION_INFO", false, fixed.Bytes(),
		node("StringFileInfo", true, nil, node("040904B0", true, nil, strs...)),
		node("VarFileInfo", true, nil, node("Translation", false, []byte{0x09, 0x04, 0xB0, 0x04})))
}

// section lays out the resource directory: type, then id, then language.
func section(res []resource) (data []byte, relocs []uint32) {
	sort.Slice(res, func(i, j int) bool {
		a, b := res[i], res[j]
		if a.typ != b.typ {
			return a.typ < b.typ
		}
		return a.id < b.id
	})
	types := map[uint32][]resource{}
	var order []uint32
	for _, r := range res {
		if len(types[r.typ]) == 0 {
			order = append(order, r.typ)
		}
		types[r.typ] = append(types[r.typ], r)
	}
	// Sizes: one root, one directory per type, one per resource.
	dirs := 16 + 8*len(order)
	for _, t := range order {
		dirs += 16 + 8*len(types[t])
	}
	dirs += len(res) * (16 + 8)
	entries := dirs                // data entries start here
	blobs := entries + 16*len(res) // raw data starts here
	out := make([]byte, blobs)
	dir := func(at, n int) { le.PutUint16(out[at+14:], uint16(n)) }
	entry := func(at int, id uint32, target int, sub bool) {
		le.PutUint32(out[at:], id)
		if sub {
			target |= 1 << 31
		}
		le.PutUint32(out[at+4:], uint32(target))
	}
	dir(0, len(order))
	next := 16 + 8*len(order)
	leaf := next
	for _, t := range order {
		leaf += 16 + 8*len(types[t])
	}
	n := 0
	for i, t := range order {
		entry(16+8*i, t, next, true)
		dir(next, len(types[t]))
		for j, r := range types[t] {
			entry(next+16+8*j, r.id, leaf, true)
			dir(leaf, 1)
			de := entries + 16*n
			entry(leaf+16, r.lang, de, false)
			for len(out)%8 != 0 {
				out = append(out, 0)
			}
			le.PutUint32(out[de:], uint32(len(out)))
			le.PutUint32(out[de+4:], uint32(len(r.data)))
			relocs = append(relocs, uint32(de))
			out = append(out, r.data...)
			leaf += 24
			n++
		}
		next += 16 + 8*len(types[t])
	}
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	return out, relocs
}

// object wraps the section in a COFF object file.
func object(machine, relocType uint16, sec []byte, relocs []uint32) []byte {
	var b bytes.Buffer
	relOff := 20 + 40 + len(sec)
	symOff := relOff + 10*len(relocs)
	_ = binary.Write(&b, le, struct {
		Machine, Sections  uint16
		Time, SymOff, Syms uint32
		OptSize, Flags     uint16
	}{machine, 1, 0, uint32(symOff), 1, 0, 0})
	_ = binary.Write(&b, le, struct {
		Name                                     [8]byte
		VSize, VAddr, Size, Off, RelOff, LineOff uint32
		Relocs, Lines                            uint16
		Flags                                    uint32
	}{[8]byte{'.', 'r', 's', 'r', 'c'}, 0, 0, uint32(len(sec)), 60, uint32(relOff), 0, uint16(len(relocs)), 0, 0x40000040})
	b.Write(sec)
	for _, r := range relocs {
		_ = binary.Write(&b, le, struct {
			Addr, Sym uint32
			Type      uint16
		}{r, 0, relocType})
	}
	_ = binary.Write(&b, le, struct {
		Name    [8]byte
		Value   uint32
		Section int16
		Type    uint16
		Class   uint8
		Aux     uint8
	}{[8]byte{'.', 'r', 's', 'r', 'c'}, 0, 1, 0, 3, 0})
	_ = binary.Write(&b, le, uint32(4)) // empty string table
	return b.Bytes()
}

func main() {
	text := "0.0.0"
	if len(os.Args) > 1 {
		text = os.Args[1]
	}
	var v [4]uint16
	for i, p := range strings.SplitN(strings.SplitN(text, "-", 2)[0], ".", 4) {
		n, _ := strconv.Atoi(p)
		v[i] = uint16(n)
	}
	four := fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])

	sizes := []int{16, 20, 24, 32, 40, 48, 64, 256}
	var res []resource
	var group bytes.Buffer
	_ = binary.Write(&group, le, [3]uint16{0, 1, uint16(len(sizes))})
	for i, s := range sizes {
		data := icon.Image(s)
		res = append(res, resource{3, uint32(i + 1), 1033, data})
		group.Write([]byte{byte(s), byte(s), 0, 0})
		_ = binary.Write(&group, le, [2]uint16{1, 32})
		_ = binary.Write(&group, le, uint32(len(data)))
		_ = binary.Write(&group, le, uint16(i+1))
	}
	res = append(res,
		resource{14, 1, 1033, group.Bytes()},
		resource{16, 1, 1033, versionInfo(v, text)},
		resource{24, 1, 1033, []byte(fmt.Sprintf(manifest, four))})
	sec, relocs := section(res)
	for _, t := range []struct {
		arch           string
		machine, reloc uint16
	}{{"amd64", 0x8664, 3}, {"arm64", 0xAA64, 2}} { // ADDR32NB for each machine
		name := "rsrc_windows_" + t.arch + ".syso"
		if err := os.WriteFile(name, object(t.machine, t.reloc, sec, relocs), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

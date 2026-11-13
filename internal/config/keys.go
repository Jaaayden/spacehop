package config

import (
	"fmt"
	"strings"

	"spacehop/internal/i18n"
)

// Virtual-key codes used across the app. Key names are what the config file
// stores; the engine works with Windows virtual-key codes.
const (
	VKBack    = 0x08
	VKTab     = 0x09
	VKEnter   = 0x0D
	VKShift   = 0x10
	VKCtrl    = 0x11
	VKAlt     = 0x12
	VKCaps    = 0x14
	VKEsc     = 0x1B
	VKSpace   = 0x20
	VKLWin    = 0x5B
	VKRWin    = 0x5C
	VKLButton = 0x01
	VKRButton = 0x02
	VKMButton = 0x04
)

var nameToVK = map[string]uint32{
	"Backspace": VKBack, "Tab": VKTab, "Enter": VKEnter, "Esc": VKEsc, "Space": VKSpace,
	"CapsLock": VKCaps,
	"PageUp":   0x21, "PageDown": 0x22, "End": 0x23, "Home": 0x24,
	"Left": 0x25, "Up": 0x26, "Right": 0x27, "Down": 0x28,
	"PrintScreen": 0x2C, "Insert": 0x2D, "Delete": 0x2E,
	";": 0xBA, "=": 0xBB, ",": 0xBC, "-": 0xBD, ".": 0xBE, "/": 0xBF, "`": 0xC0,
	"[": 0xDB, "\\": 0xDC, "]": 0xDD, "'": 0xDE,
	"VolumeMute": 0xAD, "VolumeDown": 0xAE, "VolumeUp": 0xAF,
	"MediaNext": 0xB0, "MediaPrev": 0xB1, "MediaStop": 0xB2, "MediaPlayPause": 0xB3,
	"Ctrl": VKCtrl, "Shift": VKShift, "Alt": VKAlt, "Win": VKLWin,
	"LShift": 0xA0, "RShift": 0xA1, "LCtrl": 0xA2, "RCtrl": 0xA3, "LAlt": 0xA4, "RAlt": 0xA5, "RWin": VKRWin,
	"Menu": 0x5D, "Pause": 0x13, "ScrollLock": 0x91, "NumLock": 0x90,
	"Num*": 0x6A, "Num+": 0x6B, "Num-": 0x6D, "Num.": 0x6E, "Num/": 0x6F,
}

var vkToName = map[uint32]string{}

func init() {
	for c := 'A'; c <= 'Z'; c++ {
		nameToVK[string(c)] = uint32(c)
	}
	for c := '0'; c <= '9'; c++ {
		nameToVK[string(c)] = uint32(c)
	}
	for i := 0; i <= 9; i++ {
		nameToVK[fmt.Sprintf("Num%d", i)] = uint32(0x60 + i)
	}
	for i := 1; i <= 24; i++ {
		nameToVK[fmt.Sprintf("F%d", i)] = uint32(0x70 + i - 1)
	}
	for n, v := range nameToVK {
		vkToName[v] = n
	}
}

// VK returns the virtual-key code for a key name ("A", "F5", ";", "Space").
func VK(name string) (uint32, bool) {
	if v, ok := nameToVK[name]; ok {
		return v, true
	}
	// Be forgiving about case: "a", "space", "esc".
	for n, v := range nameToVK {
		if strings.EqualFold(n, name) {
			return v, true
		}
	}
	return 0, false
}

// CanonicalKey returns the canonical spelling of a key name.
func CanonicalKey(name string) (string, bool) {
	v, ok := VK(strings.TrimSpace(name))
	if !ok {
		return "", false
	}
	return vkToName[v], true
}

// KeyName returns the display name of a virtual-key code.
func KeyName(vk uint32) string {
	if n, ok := vkToName[vk]; ok {
		return n
	}
	return fmt.Sprintf("VK%02X", vk)
}

// IsModifier reports whether vk is a Shift/Ctrl/Alt/Win key (either side).
func IsModifier(vk uint32) bool {
	switch vk {
	case VKShift, VKCtrl, VKAlt, VKLWin, VKRWin,
		0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5:
		return true
	}
	return false
}

// Modifier bits, as used for chords and the engine's modifier state.
const (
	ModShift = 1 << iota
	ModCtrl
	ModAlt
	ModWin
)

// ModBit returns the modifier bit of a Shift/Ctrl/Alt/Win key, or 0.
func ModBit(vk uint32) uint8 {
	switch vk {
	case VKShift, 0xA0, 0xA1:
		return ModShift
	case VKCtrl, 0xA2, 0xA3:
		return ModCtrl
	case VKAlt, 0xA4, 0xA5:
		return ModAlt
	case VKLWin, VKRWin:
		return ModWin
	}
	return 0
}

// Sides returns the physical keys a modifier name stands for: "Shift" is
// either Shift key, "RShift" only the right one.
func Sides(vk uint32) []uint32 {
	switch vk {
	case VKShift:
		return []uint32{0xA0, 0xA1}
	case VKCtrl:
		return []uint32{0xA2, 0xA3}
	case VKAlt:
		return []uint32{0xA4, 0xA5}
	case VKLWin:
		return []uint32{VKLWin, VKRWin}
	}
	return []uint32{vk}
}

// CanonicalChord normalises "ctrl + shift+t" to "Ctrl+Shift+T". A lone
// modifier ("RCtrl") is a valid chord with no main key.
func CanonicalChord(s string) (string, bool) {
	parts := strings.Split(s, "+")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		k, ok := CanonicalKey(p)
		if !ok {
			return "", false
		}
		if v, _ := VK(k); i < len(parts)-1 && !IsModifier(v) {
			return "", false
		}
		out = append(out, k)
	}
	return strings.Join(out, "+"), true
}

// IsExtended reports whether a key needs the "extended key" flag when it is
// synthesised.
func IsExtended(vk uint32) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2D, 0x2E, VKLWin, VKRWin, 0xA3, 0xA5, 0x5D, 0x6F:
		return true
	}
	return false
}

// ParseChord parses "Ctrl+Shift+T" into modifier codes and the main key.
func ParseChord(s string) (mods []uint32, key uint32, err error) {
	parts := strings.Split(s, "+")
	// "Ctrl++" style is not supported; "=" covers the plus key.
	for i, p := range parts {
		p = strings.TrimSpace(p)
		v, ok := VK(p)
		if !ok {
			return nil, 0, fmt.Errorf(i18n.T("未知按键 %q"), p)
		}
		if i < len(parts)-1 {
			if !IsModifier(v) {
				return nil, 0, fmt.Errorf(i18n.T("%q 不是修饰键"), p)
			}
			mods = append(mods, v)
		} else {
			key = v
		}
	}
	return mods, key, nil
}

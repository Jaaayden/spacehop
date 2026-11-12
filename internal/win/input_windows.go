package win

import (
	"unicode/utf16"
	"unsafe"

	"spacehop/internal/config"
	"spacehop/internal/engine"
)

const inputSize = 40

// sendStrokes synthesises key and mouse-button events, in order, in one
// SendInput call so nothing else can interleave.
func sendStrokes(s []engine.Stroke) {
	if len(s) == 0 {
		return
	}
	buf := make([]keybdInput, len(s))
	for i, k := range s {
		switch k.VK {
		case config.VKLButton, config.VKRButton, config.VKMButton:
			m := (*mouseInput)(unsafe.Pointer(&buf[i]))
			m.Type = inputMouse
			m.Extra = injectMagic
			switch k.VK {
			case config.VKLButton:
				m.Flags = 0x0002
			case config.VKRButton:
				m.Flags = 0x0008
			default:
				m.Flags = 0x0020
			}
			if !k.Down {
				m.Flags <<= 1
			}
		default:
			scan, _, _ := pMapVirtualKey.Call(uintptr(k.VK), 0)
			in := &buf[i]
			in.Type = inputKeyboard
			in.VK = uint16(k.VK)
			in.Scan = uint16(scan)
			in.Extra = injectMagic
			if config.IsExtended(k.VK) {
				in.Flags |= keyExtended
			}
			if !k.Down {
				in.Flags |= keyUp
			}
		}
	}
	pSendInput.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), inputSize)
}

// sendChord presses a combination such as Ctrl+Shift+T.
func sendChord(chord string) error {
	mods, key, err := config.ParseChord(chord)
	if err != nil {
		return err
	}
	var s []engine.Stroke
	for _, m := range mods {
		s = append(s, engine.Stroke{VK: m, Down: true})
	}
	s = append(s, engine.Stroke{VK: key, Down: true}, engine.Stroke{VK: key, Down: false})
	for i := len(mods) - 1; i >= 0; i-- {
		s = append(s, engine.Stroke{VK: mods[i], Down: false})
	}
	sendStrokes(s)
	return nil
}

const maxInsertText = 10000

// sendText types text as Unicode characters, independent of the keyboard
// layout and input method.
func sendText(text string) {
	units := utf16.Encode([]rune(text))
	if len(units) > maxInsertText {
		units = units[:maxInsertText]
	}
	buf := make([]keybdInput, 0, len(units)*2)
	for _, u := range units {
		if u == '\r' {
			continue
		}
		down := keybdInput{Type: inputKeyboard, Extra: injectMagic}
		if u == '\n' {
			down.VK = config.VKEnter
		} else {
			down.Scan = u
			down.Flags = keyUnicode
		}
		up := down
		up.Flags |= keyUp
		buf = append(buf, down, up)
	}
	if len(buf) > 0 {
		pSendInput.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), inputSize)
	}
}

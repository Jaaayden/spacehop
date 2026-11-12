package win

import "unsafe"

// Compile-time checks that our structures match the 64-bit Windows layouts.
// A mismatch here would fail the build rather than misbehave at run time.
var (
	_ [unsafe.Sizeof(keybdInput{}) - inputSize]byte
	_ [inputSize - unsafe.Sizeof(keybdInput{})]byte
	_ [unsafe.Sizeof(mouseInput{}) - inputSize]byte
	_ [inputSize - unsafe.Sizeof(mouseInput{})]byte
	_ [unsafe.Sizeof(notifyIconData{}) - 976]byte
	_ [976 - unsafe.Sizeof(notifyIconData{})]byte
	_ [unsafe.Sizeof(kbdllHook{}) - 24]byte
	_ [unsafe.Sizeof(msllHook{}) - 32]byte
	_ [unsafe.Sizeof(wndClassEx{}) - 80]byte
	_ [unsafe.Sizeof(msg{}) - 48]byte
)

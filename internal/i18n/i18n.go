// Package i18n translates the program's texts. The source language is
// Chinese: a text is looked up by its Chinese wording, so the code stays
// readable and a missing translation falls back to the original.
package i18n

import (
	"fmt"
	"sync/atomic"
)

var english atomic.Bool

// Set chooses the language: "en" for English, anything else for Chinese.
func Set(lang string) { english.Store(lang == "en") }

// Lang returns "en" or "zh".
func Lang() string {
	if english.Load() {
		return "en"
	}
	return "zh"
}

// T translates a text.
func T(s string) string {
	if english.Load() {
		if v, ok := en[s]; ok {
			return v
		}
	}
	return s
}

// F translates a format string and fills it in.
func F(format string, args ...any) string { return fmt.Sprintf(T(format), args...) }

// N marks a text for translation without translating it yet; use it for
// tables that are built before the language is known, and T at the point
// of use.
func N(s string) string { return s }

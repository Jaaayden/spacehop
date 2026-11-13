package ui

// Palette holds the colours of the settings window as 0xRRGGBB.
type Palette struct {
	Dark                                bool
	Bg, Surface, Raised, Line, Line2    uint32
	Text, Muted, Faint                  uint32
	Accent, AccentInk, AccentText       uint32
	AccentSoft, Danger, Warn, WarnSoft  uint32
	Key, KeyEdge                        uint32
	HintBg, HintLine, HintText, HintKey uint32
}

var (
	Light = Palette{
		Bg: 0xF3F3EF, Surface: 0xFFFFFF, Raised: 0xF8F8F5, Line: 0xE2E3DD, Line2: 0xD2D4CC,
		Text: 0x191B1F, Muted: 0x6C727D, Faint: 0x9AA0AA,
		Accent: 0xC4F236, AccentInk: 0x141A03, AccentText: 0x48760A, AccentSoft: 0xEEF8CF,
		Danger: 0xD23F31, Warn: 0xB26A00, WarnSoft: 0xFFF3DC, Key: 0xFFFFFF, KeyEdge: 0xCFD2C9,
	}
	Dark = Palette{
		Dark: true,
		Bg:   0x0E1013, Surface: 0x15171C, Raised: 0x1B1E24, Line: 0x272B33, Line2: 0x353A45,
		Text: 0xE9EBEF, Muted: 0x8B919D, Faint: 0x636A76,
		Accent: 0xC8F53C, AccentInk: 0x141A03, AccentText: 0xC8F53C, AccentSoft: 0x232B10,
		Danger: 0xFF6F61, Warn: 0xF0B24A, WarnSoft: 0x2C2414, Key: 0x23272F, KeyEdge: 0x3A404C,
	}
)

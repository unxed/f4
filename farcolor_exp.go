package main

import (
	"strconv"
	"strings"

	"github.com/unxed/vtui"
)

var namedColors = map[string]uint64{
	"F_BLACK": 0, "F_BLUE": 1, "F_GREEN": 2, "F_CYAN": 3, "F_RED": 4, "F_MAGENTA": 5, "F_BROWN": 6, "F_LIGHTGRAY": 7,
	"F_DARKGRAY": 8, "F_LIGHTBLUE": 9, "F_LIGHTGREEN": 10, "F_LIGHTCYAN": 11, "F_LIGHTRED": 12, "F_LIGHTMAGENTA": 13, "F_YELLOW": 14, "F_WHITE": 15,
	"B_BLACK": 0, "B_BLUE": 16, "B_GREEN": 32, "B_CYAN": 48, "B_RED": 64, "B_MAGENTA": 80, "B_BROWN": 96, "B_LIGHTGRAY": 112,
	"B_DARKGRAY": 128, "B_LIGHTBLUE": 144, "B_LIGHTGREEN": 160, "B_LIGHTCYAN": 176, "B_LIGHTRED": 192, "B_LIGHTMAGENTA": 208, "B_YELLOW": 224, "B_WHITE": 240,
}

// far2l default 16-color palette (tweaked from standard windows console)
var far2lPalette = []uint32{
	0x000000, 0x0028A0, 0x00A000, 0x00A0A0, 0xA00000, 0xA000A0, 0xA0A000, 0xC0C0C0,
	0x808080, 0x0055FF, 0x00FF00, 0x00FFFF, 0xFF0000, 0xFF00FF, 0xFFFF00, 0xFFFFFF,
}

// ParseFarColor converts an expression like "F_WHITE | B_CYAN" or "background:#000000" into a vtui attributes bitmask.
func ParseFarColor(expr string, defaultAttr uint64) uint64 {
	if expr == "" {
		return defaultAttr
	}

	var fg, bg uint32
	var hasFg, hasBg bool

	parts := strings.Split(expr, "|")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "foreground:#") && len(p) >= 18 {
			if val, err := strconv.ParseUint(p[12:18], 16, 32); err == nil {
				fg = uint32(val)
				hasFg = true
			}
		} else if strings.HasPrefix(p, "background:#") && len(p) >= 18 {
			if val, err := strconv.ParseUint(p[12:18], 16, 32); err == nil {
				bg = uint32(val)
				hasBg = true
			}
		} else if c, ok := namedColors[p]; ok {
			if strings.HasPrefix(p, "F_") {
				fg = far2lPalette[c]
				hasFg = true
			} else if strings.HasPrefix(p, "B_") {
				bg = far2lPalette[c>>4]
				hasBg = true
			}
		}
	}

	var attr uint64
	if hasFg && hasBg {
		attr = vtui.SetRGBBoth(attr, fg, bg)
	} else if hasFg {
		attr = vtui.SetRGBFore(defaultAttr, fg)
	} else if hasBg {
		attr = vtui.SetRGBBack(defaultAttr, bg)
	} else {
		return defaultAttr
	}
	return attr
}
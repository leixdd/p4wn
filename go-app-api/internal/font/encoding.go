package font

import (
	"strconv"
	"strings"
)

// Code→unicode tables for the standard simple-font encodings. ASCII range
// (0x20–0x7E) is identity everywhere; only deviations are stored.

var winAnsiHigh = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†',
	0x87: '‡', 0x88: 'ˆ', 0x89: '‰', 0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ',
	0x8E: 'Ž', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”',
	0x95: '•', 0x96: '–', 0x97: '—', 0x98: '˜', 0x99: '™', 0x9A: 'š',
	0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
}

var macRomanHigh = map[byte]rune{
	0x80: 'Ä', 0x81: 'Å', 0x82: 'Ç', 0x83: 'É', 0x84: 'Ñ', 0x85: 'Ö',
	0x86: 'Ü', 0x87: 'á', 0x88: 'à', 0x89: 'â', 0x8A: 'ä', 0x8B: 'ã',
	0x8C: 'å', 0x8D: 'ç', 0x8E: 'é', 0x8F: 'è', 0x90: 'ê', 0x91: 'ë',
	0x92: 'í', 0x93: 'ì', 0x94: 'î', 0x95: 'ï', 0x96: 'ñ', 0x97: 'ó',
	0x98: 'ò', 0x99: 'ô', 0x9A: 'ö', 0x9B: 'õ', 0x9C: 'ú', 0x9D: 'ù',
	0x9E: 'û', 0x9F: 'ü', 0xA0: '†', 0xA1: '°', 0xA2: '¢', 0xA3: '£',
	0xA4: '§', 0xA5: '•', 0xA6: '¶', 0xA7: 'ß', 0xA8: '®', 0xA9: '©',
	0xAA: '™', 0xAB: '´', 0xAC: '¨', 0xAE: 'Æ', 0xAF: 'Ø', 0xB0: '∞',
	0xB1: '±', 0xB4: '¥', 0xB5: 'µ', 0xBB: 'ª', 0xBC: 'º', 0xBE: 'æ',
	0xBF: 'ø', 0xC0: '¿', 0xC1: '¡', 0xC2: '¬', 0xC4: 'ƒ', 0xC7: '«',
	0xC8: '»', 0xC9: '…', 0xCA: ' ', 0xCB: 'À', 0xCC: 'Ã', 0xCD: 'Õ',
	0xCE: 'Œ', 0xCF: 'œ', 0xD0: '–', 0xD1: '—', 0xD2: '“', 0xD3: '”',
	0xD4: '‘', 0xD5: '’', 0xD6: '÷', 0xD8: 'ÿ', 0xD9: 'Ÿ',
	0xDA: '⁄', 0xDB: '€', 0xDC: '‹', 0xDD: '›', 0xE1: '·', 0xE2: '‚',
	0xE3: '„', 0xE4: '‰', 0xE5: 'Â', 0xE6: 'Ê', 0xE7: 'Á', 0xE8: 'Ë',
	0xE9: 'È', 0xEA: 'Í', 0xEB: 'Î', 0xEC: 'Ï', 0xED: 'Ì', 0xEE: 'Ó',
	0xEF: 'Ô', 0xF1: 'Ò', 0xF2: 'Ú', 0xF3: 'Û', 0xF4: 'Ù', 0xF5: 'ı',
	0xF6: 'ˆ', 0xF7: '˜', 0xF8: '¯',
}

var standardHigh = map[byte]rune{
	0xA1: '¡', 0xA2: '¢', 0xA3: '£', 0xA4: '⁄', 0xA5: '¥', 0xA7: '§',
	0xA8: '¤', 0xA9: '\'', 0xAA: '“', 0xAB: '«', 0xAC: '‹', 0xAD: '›',
	0xAE: 'ﬁ', 0xAF: 'ﬂ', 0xB1: '–', 0xB2: '†', 0xB3: '‡', 0xB4: '·',
	0xB6: '¶', 0xB7: '•', 0xB8: '‚', 0xB9: '„', 0xBA: '”', 0xBB: '»',
	0xBC: '…', 0xBD: '‰', 0xBF: '¿', 0xC1: '`', 0xC2: '´', 0xC3: 'ˆ',
	0xC4: '˜', 0xC5: '¯', 0xC8: '¨', 0xCA: '˚', 0xCB: '¸', 0xCF: 'ˇ',
	0xD0: '—', 0xE1: 'Æ', 0xE3: 'ª', 0xE8: 'Ł', 0xE9: 'Ø', 0xEA: 'Œ',
	0xEB: 'º', 0xF1: 'æ', 0xF5: 'ı', 0xF8: 'ł', 0xF9: 'ø', 0xFA: 'œ',
	0xFB: 'ß',
}

// encodingToUnicode maps a byte code through the named base encoding.
// Codes 0x20–0x7E are (nearly) ASCII in all standard encodings.
func encodingToUnicode(encoding string, code byte) rune {
	if code >= 0x20 && code <= 0x7E {
		// Standard encoding deviates on quoteright (0x27) and grave (0x60)
		if encoding == "StandardEncoding" {
			switch code {
			case 0x27:
				return '’'
			case 0x60:
				return '‘'
			}
		}
		return rune(code)
	}
	switch encoding {
	case "WinAnsiEncoding":
		if r, ok := winAnsiHigh[code]; ok {
			return r
		}
		if code >= 0xA0 {
			return rune(code) // latin-1 range
		}
	case "MacRomanEncoding":
		if r, ok := macRomanHigh[code]; ok {
			return r
		}
	case "StandardEncoding":
		if r, ok := standardHigh[code]; ok {
			return r
		}
	default:
		if code >= 0xA0 {
			return rune(code)
		}
	}
	return 0
}

// glyphNameToUnicode resolves a PostScript glyph name (Adobe Glyph List
// subset + uniXXXX/uXXXX forms + digit shorthand).
func glyphNameToUnicode(name string) rune {
	if r, ok := aglSubset[name]; ok {
		return r
	}
	if strings.HasPrefix(name, "uni") && len(name) >= 7 {
		if v, err := strconv.ParseUint(name[3:7], 16, 32); err == nil {
			return rune(v)
		}
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			return rune(v)
		}
	}
	// single-letter names are themselves ("a" → 'a')
	if len(name) == 1 {
		return rune(name[0])
	}
	// LaTeX-style "gXX" subset names carry no unicode
	return 0
}

// aglSubset covers Latin text, digits, punctuation and common symbols —
// the names that show up in /Differences arrays of real documents.
var aglSubset = map[string]rune{
	"space": ' ', "exclam": '!', "quotedbl": '"', "numbersign": '#',
	"dollar": '$', "percent": '%', "ampersand": '&', "quotesingle": '\'',
	"parenleft": '(', "parenright": ')', "asterisk": '*', "plus": '+',
	"comma": ',', "hyphen": '-', "period": '.', "slash": '/',
	"zero": '0', "one": '1', "two": '2', "three": '3', "four": '4',
	"five": '5', "six": '6', "seven": '7', "eight": '8', "nine": '9',
	"colon": ':', "semicolon": ';', "less": '<', "equal": '=',
	"greater": '>', "question": '?', "at": '@',
	"bracketleft": '[', "backslash": '\\', "bracketright": ']',
	"asciicircum": '^', "underscore": '_', "grave": '`',
	"braceleft": '{', "bar": '|', "braceright": '}', "asciitilde": '~',
	"quoteleft": '‘', "quoteright": '’',
	"quotedblleft": '“', "quotedblright": '”',
	"quotesinglbase": '‚', "quotedblbase": '„',
	"endash": '–', "emdash": '—', "bullet": '•', "ellipsis": '…',
	"dagger": '†', "daggerdbl": '‡', "periodcentered": '·',
	"guillemotleft": '«', "guillemotright": '»',
	"guilsinglleft": '‹', "guilsinglright": '›',
	"fi": 'ﬁ', "fl": 'ﬂ', "ff": 'ﬀ', "ffi": 'ﬃ', "ffl": 'ﬄ',
	"cent": '¢', "sterling": '£', "yen": '¥', "Euro": '€', "currency": '¤',
	"section": '§', "paragraph": '¶', "copyright": '©', "registered": '®',
	"trademark": '™', "degree": '°', "plusminus": '±', "multiply": '×',
	"divide": '÷', "minus": '−', "fraction": '⁄', "florin": 'ƒ',
	"exclamdown": '¡', "questiondown": '¿',
	"ordfeminine": 'ª', "ordmasculine": 'º',
	"onehalf": '½', "onequarter": '¼', "threequarters": '¾',
	"onesuperior": '¹', "twosuperior": '²', "threesuperior": '³',
	"mu": 'µ', "logicalnot": '¬', "brokenbar": '¦', "macron": '¯',
	"acute": '´', "dieresis": '¨', "cedilla": '¸', "circumflex": 'ˆ',
	"tilde": '˜', "caron": 'ˇ', "breve": '˘', "dotaccent": '˙',
	"ring": '˚', "hungarumlaut": '˝', "ogonek": '˛',
	"Agrave": 'À', "Aacute": 'Á', "Acircumflex": 'Â', "Atilde": 'Ã',
	"Adieresis": 'Ä', "Aring": 'Å', "AE": 'Æ', "Ccedilla": 'Ç',
	"Egrave": 'È', "Eacute": 'É', "Ecircumflex": 'Ê', "Edieresis": 'Ë',
	"Igrave": 'Ì', "Iacute": 'Í', "Icircumflex": 'Î', "Idieresis": 'Ï',
	"Eth": 'Ð', "Ntilde": 'Ñ', "Ograve": 'Ò', "Oacute": 'Ó',
	"Ocircumflex": 'Ô', "Otilde": 'Õ', "Odieresis": 'Ö', "Oslash": 'Ø',
	"Ugrave": 'Ù', "Uacute": 'Ú', "Ucircumflex": 'Û', "Udieresis": 'Ü',
	"Yacute": 'Ý', "Thorn": 'Þ', "germandbls": 'ß',
	"agrave": 'à', "aacute": 'á', "acircumflex": 'â', "atilde": 'ã',
	"adieresis": 'ä', "aring": 'å', "ae": 'æ', "ccedilla": 'ç',
	"egrave": 'è', "eacute": 'é', "ecircumflex": 'ê', "edieresis": 'ë',
	"igrave": 'ì', "iacute": 'í', "icircumflex": 'î', "idieresis": 'ï',
	"eth": 'ð', "ntilde": 'ñ', "ograve": 'ò', "oacute": 'ó',
	"ocircumflex": 'ô', "otilde": 'õ', "odieresis": 'ö', "oslash": 'ø',
	"ugrave": 'ù', "uacute": 'ú', "ucircumflex": 'û', "udieresis": 'ü',
	"yacute": 'ý', "thorn": 'þ', "ydieresis": 'ÿ',
	"Lslash": 'Ł', "lslash": 'ł', "OE": 'Œ', "oe": 'œ',
	"Scaron": 'Š', "scaron": 'š', "Zcaron": 'Ž', "zcaron": 'ž',
	"Ydieresis": 'Ÿ', "dotlessi": 'ı', "perthousand": '‰',
	"nbspace": ' ', "sfthyphen": '­',
	// Symbol font basics (math glyphs common in TeX output)
	"alpha": 'α', "beta": 'β', "gamma": 'γ', "delta": 'δ', "epsilon": 'ε',
	"lambda": 'λ', "pi": 'π', "sigma": 'σ', "theta": 'θ', "phi": 'φ',
	"omega": 'ω', "Delta": 'Δ', "Omega": 'Ω', "Sigma": 'Σ', "Pi": 'Π',
	"summation": '∑', "product": '∏', "integral": '∫', "infinity": '∞',
	"radical": '√', "partialdiff": '∂', "lessequal": '≤',
	"greaterequal": '≥', "notequal": '≠', "approxequal": '≈',
	"arrowleft": '←', "arrowright": '→', "arrowup": '↑', "arrowdown": '↓',
}

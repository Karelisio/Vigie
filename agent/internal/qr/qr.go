// Package qr affiche un QR code dans un terminal (demi-blocs Unicode).
package qr

import (
	"strings"

	"rsc.io/qr"
)

// Render renvoie le QR code sous forme de texte. Par défaut, les modules clairs
// sont dessinés en blocs (adapté aux visionneuses de logs sur fond sombre) ;
// invert inverse le rendu pour les fonds clairs.
func Render(text string, invert bool) (string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", err
	}
	const quiet = 2
	size := code.Size
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= size || y >= size {
			return false
		}
		return code.Black(x, y)
	}
	// filled = module dessiné en bloc
	filled := func(x, y int) bool { return dark(x, y) == invert }
	var b strings.Builder
	for y := -quiet; y < size+quiet; y += 2 {
		for x := -quiet; x < size+quiet; x++ {
			top, bottom := filled(x, y), filled(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

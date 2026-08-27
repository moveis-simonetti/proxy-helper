//go:build wingui

// Tradução do design system fluaui (Tailwind/gluestack) para um fyne.Theme.
//
// Só as escalas `fluaui.*` do tema original têm valores literais; as escalas
// primary/secondary/error/... são CSS custom properties
// (`rgb(var(--color-primary-500))`) e não carregam cor nenhuma fora do
// browser. Por isso o mapeamento abaixo parte das `fluaui.*` e liga cada
// papel semântico do Fyne a um tom concreto.
package wingui

import (
	_ "embed"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

//go:embed fonts/Poppins-Regular.ttf
var poppinsRegular []byte

//go:embed fonts/Poppins-Medium.ttf
var poppinsMedium []byte

//go:embed fonts/Poppins-SemiBold.ttf
var poppinsSemiBold []byte

//go:embed fonts/Poppins-Bold.ttf
var poppinsBold []byte

//go:embed fonts/Poppins-Italic.ttf
var poppinsItalic []byte

//go:embed fonts/Poppins-BoldItalic.ttf
var poppinsBoldItalic []byte

// hex converte "#rrggbb" em color.NRGBA opaco.
func hex(s string) color.NRGBA {
	var r, g, b uint8
	v := func(c byte) uint8 {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10
		}
		return 0
	}
	s = s[1:] // descarta '#'
	r = v(s[0])<<4 | v(s[1])
	g = v(s[2])<<4 | v(s[3])
	b = v(s[4])<<4 | v(s[5])
	return color.NRGBA{R: r, G: g, B: b, A: 255}
}

// Paleta fluaui — só os tons usados pelo tema claro. A paleta completa
// do design system está em docs/tema-fluaui.md.
var (
	// Cor padrão da marca. brand.600 (#ea1430) é o tom escuro da escala,
	// disponível no design system se um estado pressionado precisar dele.
	brand500 = hex("#fd5260")

	red600 = hex("#f8131c")

	blue600 = hex("#2283EE")

	green600 = hex("#46921e")

	orange500 = hex("#f5851a")

	grey0   = hex("#ffffff")
	grey25  = hex("#f9f9fa")
	grey50  = hex("#f6f6f6")
	grey100 = hex("#e7e7e7")
	grey200 = hex("#d1d1d1")
	grey300 = hex("#b0b0b0")
	grey400 = hex("#858585")
	grey950 = hex("#262626")

	typographyBlack = hex("#181718")

	backgroundLight = hex("#FBFBFB")
)

func alpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

type fluaTheme struct{}

var _ fyne.Theme = (*fluaTheme)(nil)

// Color ignora a variante do sistema de propósito: o app é sempre claro.
// Um app que muda de cara conforme o tema do SO exige manter duas paletas
// corretas, e o público de gestão não ganha nada com isso.
func (fluaTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return fluaLight(n)
}

func fluaLight(n fyne.ThemeColorName) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return backgroundLight
	case theme.ColorNameForeground:
		return typographyBlack
	case theme.ColorNameForegroundOnPrimary:
		return grey0
	case theme.ColorNamePrimary:
		return brand500
	case theme.ColorNameHyperlink:
		return blue600
	case theme.ColorNameError:
		return red600
	case theme.ColorNameSuccess:
		return green600
	case theme.ColorNameWarning:
		return orange500
	case theme.ColorNameButton:
		return grey50
	case theme.ColorNameDisabledButton:
		return grey100
	case theme.ColorNameDisabled:
		return grey300
	case theme.ColorNamePlaceHolder:
		return grey400
	case theme.ColorNameInputBackground:
		return grey25
	case theme.ColorNameInputBorder:
		return grey200
	case theme.ColorNameSeparator:
		return grey100
	case theme.ColorNameHeaderBackground:
		return grey25
	case theme.ColorNameMenuBackground:
		return grey0
	case theme.ColorNameOverlayBackground:
		return grey0
	case theme.ColorNameScrollBar:
		return alpha(grey400, 0x66)
	case theme.ColorNameHover:
		return alpha(grey200, 0x55)
	case theme.ColorNamePressed:
		return alpha(grey300, 0x66)
	case theme.ColorNameFocus:
		// Deliberately NOT the brand colour. The brand is red, and a red
		// ring around the field someone is typing in reads as "you got this
		// wrong" — the exact meaning the error state needs to own.
		return alpha(blue600, 0x55)
	case theme.ColorNameSelection:
		return alpha(blue600, 0x33)
	case theme.ColorNameShadow:
		// boxShadow do design system: rgba(38,38,38,0.20) == grey950 a 20%.
		return alpha(grey950, 0x33)
	}
	return theme.DefaultTheme().Color(n, theme.VariantLight)
}

func (fluaTheme) Font(s fyne.TextStyle) fyne.Resource {
	switch {
	case s.Monospace:
		return theme.DefaultTheme().Font(s)
	case s.Symbol:
		return theme.DefaultTheme().Font(s)
	case s.Bold && s.Italic:
		return fyne.NewStaticResource("Poppins-BoldItalic.ttf", poppinsBoldItalic)
	case s.Bold:
		return fyne.NewStaticResource("Poppins-Bold.ttf", poppinsBold)
	case s.Italic:
		return fyne.NewStaticResource("Poppins-Italic.ttf", poppinsItalic)
	}
	return fyne.NewStaticResource("Poppins-Regular.ttf", poppinsRegular)
}

// Pesos intermediários do design system, para uso direto onde a API do Fyne
// aceita um recurso de fonte.
var (
	FontMedium   = fyne.NewStaticResource("Poppins-Medium.ttf", poppinsMedium)
	FontSemiBold = fyne.NewStaticResource("Poppins-SemiBold.ttf", poppinsSemiBold)
)

func (fluaTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (fluaTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		// fontSize '2xs' do design system.
		return 10
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 17
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 8
	}
	return theme.DefaultTheme().Size(n)
}

//go:build wingui

package wingui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Colours the screens reach for by name, so a change lands in one place.
var (
	colorForeground = typographyBlack
	colorSuccess    = green600
	colorError      = red600
	colorMuted      = grey400
)

// noticeBackgrounds are the tinted panels behind a message. Success is
// green and failure is red — never the brand colour, which is also red and
// would make "tudo certo" read as an error.
var (
	successFill   = color.NRGBA{R: 0xf2, G: 0xfb, B: 0xea, A: 0xff}
	successStroke = color.NRGBA{R: 0xc5, G: 0xef, B: 0xa7, A: 0xff}
	errorFill     = color.NRGBA{R: 0xff, G: 0xf0, B: 0xf1, A: 0xff}
	errorStroke   = color.NRGBA{R: 0xff, G: 0xbb, B: 0xc0, A: 0xff}
)

// newNoticeCard renders one Message as a tinted panel.
//
// Title and body are segments of a single RichText rather than a canvas.Text
// beside a widget.Label: those two paint their text at different insets, so
// mixing them left the title hard against the panel edge and the body
// indented under it. One widget means one alignment, and RichText wraps.
func newNoticeCard(msg Message) fyne.CanvasObject {
	fill, stroke := successFill, successStroke
	titleColor := theme.ColorNameSuccess
	if msg.Bad {
		fill, stroke = errorFill, errorStroke
		titleColor = theme.ColorNameError
	}

	panel := canvas.NewRectangle(fill)
	panel.StrokeColor = stroke
	panel.StrokeWidth = 1
	panel.CornerRadius = 8

	text := widget.NewRichText(
		&widget.TextSegment{
			Text:  msg.Title,
			Style: widget.RichTextStyle{ColorName: titleColor, TextStyle: fyne.TextStyle{Bold: true}},
		},
		&widget.TextSegment{
			Text:  msg.Body,
			Style: widget.RichTextStyle{ColorName: theme.ColorNameForeground},
		},
	)
	text.Wrapping = fyne.TextWrapWord

	return container.NewStack(panel, text)
}

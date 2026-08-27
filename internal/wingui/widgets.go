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
	colorMuted      = grey500
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

// sealFills are the tinted discs behind the state icon.
var (
	sealOnFill    = color.NRGBA{R: 0xf2, G: 0xfb, B: 0xea, A: 0xff}
	sealOnStroke  = color.NRGBA{R: 0xc5, G: 0xef, B: 0xa7, A: 0xff}
	sealOffFill   = color.NRGBA{R: 0xf6, G: 0xf6, B: 0xf6, A: 0xff}
	sealOffStroke = color.NRGBA{R: 0xe7, G: 0xe7, B: 0xe7, A: 0xff}
)

const sealSize = 96

// newStateSeal is the disc that carries the on/off state.
//
// A large, coloured shape rather than text alone: the audience reads this
// window from across a desk to answer one question, and shape and colour
// answer it before any word is read.
func newStateSeal(on bool, icon fyne.Resource) fyne.CanvasObject {
	fill, stroke := sealOffFill, sealOffStroke
	if on {
		fill, stroke = sealOnFill, sealOnStroke
	}

	disc := canvas.NewCircle(fill)
	disc.StrokeColor = stroke
	disc.StrokeWidth = 2

	image := canvas.NewImageFromResource(icon)
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(sealSize/2, sealSize/2))

	seal := container.NewStack(disc, container.NewPadded(image))
	seal.Resize(fyne.NewSize(sealSize, sealSize))

	// Centred in a fixed box, so the disc does not stretch to the window.
	box := container.New(&fixedSize{width: sealSize, height: sealSize}, seal)
	return container.NewCenter(box)
}

// fixedSize keeps its child at one size regardless of the space available.
type fixedSize struct {
	width, height float32
}

func (f *fixedSize) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(f.width, f.height)
}

func (f *fixedSize) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(f.width, f.height))
	}
}

// stateSize and colorExplain are the main screen's headline and the grey of
// its explanatory line, both taken from the canvas.
const stateSize = 30

var colorExplain = hex("#5d5d5d")

// setText updates a canvas.Text and redraws it. canvas.Text has no SetText,
// unlike widget.Label, and forgetting the Refresh leaves the old string on
// screen — a stale label on the one screen whose job is to report state.
func setText(t *canvas.Text, value string) {
	t.Text = value
	t.Refresh()
}

// Vertical rhythm taken from the canvas. Fyne's widgets are more compact
// than the CSS boxes they were drawn as, so the spacing has to be stated
// rather than inherited from padding — without it the window either bunches
// up at the top or, with a bottom-anchored footer, opens a hole in the
// middle.
const (
	spaceAboveSeal    = 28
	spaceSealToState  = 10
	spaceStateToText  = 4
	spaceAboveAction  = 26
	spaceActionToCard = 14
)

// vSpace is a fixed vertical gap.
func vSpace(height float32) fyne.CanvasObject {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, height))
	return spacer
}

// insetLayout applies explicit padding on each side.
//
// container.NewPadded uses the theme's single padding value on all four
// sides, which is not what the canvas specifies: its cards are 14px top and
// bottom, 16px left and right. Using the theme value made the profile card
// look cramped against its own border.
type insetLayout struct {
	top, right, bottom, left float32
}

func (i *insetLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var min fyne.Size
	for _, o := range objects {
		min = min.Max(o.MinSize())
	}
	return fyne.NewSize(min.Width+i.left+i.right, min.Height+i.top+i.bottom)
}

func (i *insetLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	inner := fyne.NewSize(size.Width-i.left-i.right, size.Height-i.top-i.bottom)
	for _, o := range objects {
		o.Move(fyne.NewPos(i.left, i.top))
		o.Resize(inner)
	}
}

// newInset wraps content with the canvas's card padding.
func newInset(content fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&insetLayout{top: 14, right: 16, bottom: 14, left: 16}, content)
}

// tightStack stacks children with an exact gap, unlike VBox which uses the
// theme's padding between every pair.
type tightStack struct {
	gap float32
}

func (t *tightStack) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var width, height float32
	for i, o := range objects {
		min := o.MinSize()
		if min.Width > width {
			width = min.Width
		}
		height += min.Height
		if i > 0 {
			height += t.gap
		}
	}
	return fyne.NewSize(width, height)
}

func (t *tightStack) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i, o := range objects {
		if i > 0 {
			y += t.gap
		}
		height := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, height))
		y += height
	}
}

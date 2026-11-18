package intelligentui

import "github.com/z-chenhao/assistant/internal/delivery/uidocv1"

type Component = uidocv1.Component
type Widget uidocv1.Document
type nativeProps = uidocv1.NativeProps
type FormProps = uidocv1.FormProps
type FormField = uidocv1.FormField
type LineProps = uidocv1.LineProps

func ParseWidget(raw string) (Widget, bool) {
	document, ok := uidocv1.Parse(raw)
	return Widget(document), ok
}

func validNative(c Component) bool {
	return uidocv1.ValidComponent(c.Name, c.Props)
}

func (w Widget) Readable() string {
	if w.Text == "" {
		return w.Fallback
	}
	return w.Text + "\n\n" + w.Fallback
}

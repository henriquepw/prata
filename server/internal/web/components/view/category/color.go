package category

import "github.com/henriquepw/prata/server/internal/web/prefs"

// Color is a schema color, whose label is the i18n key "color." + Value, and its text color class, spelled
// out here for Tailwind.
type Color struct {
	prefs.Option
	Class string
}

var Colors = []Color{
	{prefs.Option{Value: "GRAY"}, "text-cat-gray"},
	{prefs.Option{Value: "RED"}, "text-cat-red"},
	{prefs.Option{Value: "YELLOW"}, "text-cat-yellow"},
	{prefs.Option{Value: "BLUE"}, "text-cat-blue"},
	{prefs.Option{Value: "GREEN"}, "text-cat-green"},
}

// ColorClass is the text color class of a schema color.
func ColorClass(value string) string {
	color, _ := prefs.Find(Colors, value)

	return color.Class
}

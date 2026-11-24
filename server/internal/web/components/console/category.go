package console

import "github.com/henriquepw/prata/server/internal/web/prefs"

// CategoryColor is a schema color, whose label is the i18n key "color." + Value, and its text color class, spelled
// out here for Tailwind.
type CategoryColor struct {
	prefs.Option
	Class string
}

var CategoryColors = []CategoryColor{
	{prefs.Option{Value: "GRAY"}, "text-cat-gray"},
	{prefs.Option{Value: "RED"}, "text-cat-red"},
	{prefs.Option{Value: "YELLOW"}, "text-cat-yellow"},
	{prefs.Option{Value: "BLUE"}, "text-cat-blue"},
	{prefs.Option{Value: "GREEN"}, "text-cat-green"},
}

// ColorClass is the text color class of a schema color.
func ColorClass(value string) string {
	color, _ := prefs.Find(CategoryColors, value)

	return color.Class
}

// KindClass is the color of an amount of kind: cat-green for INCOME, destructive for OUTCOME.
func KindClass(kind string) string {
	if kind == "INCOME" {
		return "text-cat-green"
	}

	return "text-destructive"
}

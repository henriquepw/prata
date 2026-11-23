package homepage

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/oklog/ulid/v2"
)

// launchLine is a line of the Mês prompt read as a transaction: Amount is 0 when the line has none, Date is
// "YYYY-MM-DD", Category and Tag are zero when the line names none, Unknown holds every "@x" or "#x" that names
// nothing, and Suggestions the completions of the "@" or "#" the line ends in.
type launchLine struct {
	Kind, Description, Date string
	Amount                  int64
	Category                store.Category
	Tag                     store.Tag
	Unknown, Suggestions    []string
}

// accents folds the accented letters of pt-BR into plain ones.
var accents = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o", "õ", "o", "ú", "u", "ü", "u", "ç", "c")

// fold is s in lower case, without accents and spaces, as "@" and "#" match and complete names.
func fold(s string) string {
	return strings.ReplaceAll(accents.Replace(strings.ToLower(s)), " ", "")
}

// parseLaunch reads a prompt line, as "-45,90 padaria ontem @merc #casa": the first number in the request's Formato
// numérico is the amount, an outcome unless signed "+"; "@x" is the first category and "#x" the first tag whose
// folded name starts with x; "hoje", "ontem" and "dd/mm" in year are the date, today by default; the rest is the
// description.
func parseLaunch(ctx context.Context, line string, categories []store.Category, tags []store.Tag, today time.Time, year int) launchLine {
	categoryNames, tagNames := names(categories, func(c store.Category) string { return c.Name }), names(tags, func(t store.Tag) string { return t.Name })
	l, words := launchLine{Kind: "OUTCOME", Date: today.Format(time.DateOnly)}, []string{}
	for _, word := range strings.Fields(line) {
		cents, isAmount := shared.ParseAmount(ctx, strings.TrimLeft(word, "+-−"))
		date, err := time.Parse("2/1/2006", word+"/"+strconv.Itoa(year))
		category, tag := prefixed(categoryNames, word), prefixed(tagNames, word)
		switch {
		case l.Amount == 0 && isAmount && cents > 0:
			l.Amount = cents
			if word[0] == '+' {
				l.Kind = "INCOME"
			}
		case word[0] == '@' && category >= 0:
			l.Category = categories[category]
		case word[0] == '#' && tag >= 0:
			l.Tag = tags[tag]
		case word[0] == '@' || word[0] == '#':
			l.Unknown = append(l.Unknown, word)
		case fold(word) == fold(i18n.T(ctx, "month.today")):
			l.Date = today.Format(time.DateOnly)
		case fold(word) == fold(i18n.T(ctx, "launch.yesterday")):
			l.Date = today.AddDate(0, 0, -1).Format(time.DateOnly)
		case err == nil:
			l.Date = date.Format(time.DateOnly)
		default:
			words = append(words, word)
		}
	}

	l.Description = strings.Join(words, " ")
	if l.Kind == "INCOME" {
		l.Category = store.Category{}
	} else if l.Category.ID == "" {
		l.Category = shared.DefaultCategory(categories)
	}

	switch last := line[strings.LastIndexAny(line, " \t")+1:]; {
	case strings.HasPrefix(last, "@"):
		l.Suggestions = completions(categoryNames, last)
	case strings.HasPrefix(last, "#"):
		l.Suggestions = completions(tagNames, last)
	}

	return l
}

// names maps items to their names.
func names[T any](items []T, name func(T) string) []string {
	all := make([]string, len(items))
	for i, item := range items {
		all[i] = name(item)
	}

	return all
}

// prefixed is the index of the first name whose folded form starts with the word after its "@" or "#", -1 when none
// does or the word is bare.
func prefixed(names []string, word string) int {
	if len(word) == 1 {
		return -1
	}

	return slices.IndexFunc(names, func(name string) bool { return strings.HasPrefix(fold(name), fold(word[1:])) })
}

// completions are the names whose folded form starts with the word after its "@" or "#", folded behind that "@" or
// "#", as "@mercado".
func completions(names []string, word string) []string {
	var all []string
	for _, name := range names {
		if strings.HasPrefix(fold(name), fold(word[1:])) {
			all = append(all, word[:1]+fold(name))
		}
	}

	return all
}

// isValid reports whether the line has an amount and every "@x" and "#x" names something.
func (l launchLine) isValid() bool { return l.Amount > 0 && len(l.Unknown) == 0 }

// readLaunch parses the request's prompt line against the live categories and tags, today in the user's time zone
// and the year of the month on screen, answering 500 when it cannot read them.
func (p Page) readLaunch(w http.ResponseWriter, r *http.Request) (launchLine, bool) {
	categories, ok := shared.LiveCategories(w, r, p.db)
	if !ok {
		return launchLine{}, false
	}

	tags, ok := shared.LiveTags(w, r, p.db)
	if !ok {
		return launchLine{}, false
	}

	now := time.Now().In(shared.Location(r))

	return parseLaunch(r.Context(), r.FormValue("line"), categories, tags, now, shownMonth(r, now).Year()), true
}

// LaunchPreview answers the preview of the request's prompt line.
func (p Page) LaunchPreview(w http.ResponseWriter, r *http.Request) {
	l, ok := p.readLaunch(w, r)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, launchPreview(r.FormValue("line"), l))
}

// Launch creates the transaction the prompt line reads as, at local midnight of its date in UTC, and answers the
// month on screen with the new row flashing; a line with problems answers 422 with its preview and creates nothing.
func (p Page) Launch(w http.ResponseWriter, r *http.Request) {
	l, ok := p.readLaunch(w, r)
	if !ok {
		return
	}

	if !l.isValid() {
		shared.Render(w, r, http.StatusUnprocessableEntity, launchPreview(r.FormValue("line"), l))
		return
	}

	occurredAt, _ := shared.ParseDate(l.Date, shared.Location(r))
	change := store.Change{Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: store.KindCreate, Fields: map[string]any{
		"kind": l.Kind, "amount": l.Amount, "description": shared.OrNil(l.Description), "occurred_at": occurredAt,
		"category_id": shared.OrNil(l.Category.ID), "tag_id": shared.OrNil(l.Tag.ID),
	}}
	p.CreateInMonth(w, r, change, nil)
}

package web

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
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
		cents, isAmount := parseAmount(ctx, strings.TrimLeft(word, "+-−"))
		date, err := time.Parse("2/1/2006", word+"/"+strconv.Itoa(year))
		switch {
		case l.Amount == 0 && isAmount && cents > 0:
			l.Amount = cents
			if word[0] == '+' {
				l.Kind = "INCOME"
			}
		case word[0] == '@' && prefixed(categoryNames, word) >= 0:
			l.Category = categories[prefixed(categoryNames, word)]
		case word[0] == '#' && prefixed(tagNames, word) >= 0:
			l.Tag = tags[prefixed(tagNames, word)]
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

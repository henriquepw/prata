// Package importfile reads the bank files the user imports into Movements.
package importfile

import (
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// ErrUnrecognized means the file is not in a format this package reads.
var ErrUnrecognized = errors.New("unrecognized file")

// Bank account kinds a statement declares.
const (
	Checking = "CHECKING"
	Card     = "CARD"
)

// Statement is the lines of one bank account declared by the file, with the currency it declares, empty when none.
type Statement struct {
	Institution string
	Account     string
	Kind        string
	Currency    string
	Rows        []Row
}

// Row is one line of a file: Amount in positive cents, Kind INCOME or OUTCOME, and Date as YYYY-MM-DD, as the file
// has it.
type Row struct {
	ID          string
	Description string
	Kind        string
	Amount      int64
	Date        string
}

// Parse reads a file in any format this package knows, which it recognizes by the content.
func Parse(content []byte) ([]Statement, error) {
	if statements, err := parseOFX(content); err == nil {
		return statements, nil
	}

	return parseNubankCSV(content)
}

// ofxLine is a Row as read, with the TRNTYPE the file gives it.
type ofxLine struct {
	Row
	Type string
}

// tag matches an OFX tag with the text after it, which is the whole value in SGML (1.x) and in XML (2.x) alike.
var tag = regexp.MustCompile(`<([A-Za-z0-9.]+)>\s*([^<]*)`)

// parseOFX reads an OFX 1.x or 2.x file of bank accounts and credit cards, in UTF-8 or Windows-1252. A statement
// without a BANKID, as a card's, takes the institution from the sign-on ORG.
func parseOFX(content []byte) ([]Statement, error) {
	text := decode(content)
	if !strings.Contains(strings.ToUpper(text), "<OFX>") {
		return nil, ErrUnrecognized
	}

	var statements []Statement
	var row *ofxLine
	var rows []ofxLine
	var org string
	flush := func() {
		if len(statements) == 0 {
			return
		}

		last := &statements[len(statements)-1]
		for _, r := range rows {
			last.Rows = append(last.Rows, withKind(r.Row, r.Type, last.Kind))
		}

		rows = nil
	}

	for _, m := range tag.FindAllStringSubmatch(text, -1) {
		name, value := strings.ToUpper(m[1]), html.UnescapeString(strings.TrimSpace(m[2]))
		switch name {
		case "ORG":
			org = value
		case "STMTRS", "CCSTMTRS":
			flush()
			row = nil
			statements = append(statements, Statement{})
		case "BANKACCTFROM":
			statements[len(statements)-1].Kind = Checking
		case "CCACCTFROM":
			statements[len(statements)-1].Kind = Card
		case "STMTTRN":
			rows = append(rows, ofxLine{})
			row = &rows[len(rows)-1]
		}

		if len(statements) == 0 {
			continue
		}

		current := &statements[len(statements)-1]
		switch name {
		case "BANKID":
			current.Institution = value
		case "ACCTID":
			current.Account = value
		case "CURDEF":
			current.Currency = strings.ToUpper(value)
		}

		if row == nil {
			continue
		}

		switch name {
		case "FITID":
			row.ID = value
		case "TRNTYPE":
			row.Type = strings.ToUpper(value)
		case "TRNAMT":
			cents, err := parseCents(value)
			if err != nil {
				return nil, ErrUnrecognized
			}

			row.Amount = cents
		case "DTPOSTED":
			if len(value) < 8 {
				return nil, ErrUnrecognized
			}

			date, err := time.Parse("20060102", value[:8])
			if err != nil {
				return nil, ErrUnrecognized
			}

			row.Date = date.Format(time.DateOnly)
		case "NAME":
			row.Description = value
		case "MEMO":
			if row.Description == "" {
				row.Description = value
			}
		}
	}

	flush()

	if len(statements) == 0 {
		return nil, ErrUnrecognized
	}

	for i, statement := range statements {
		statements[i].Institution = cmp.Or(statement.Institution, org)
		for _, row := range statement.Rows {
			if row.ID == "" || row.Date == "" {
				return nil, ErrUnrecognized
			}
		}
	}

	return statements, nil
}

// withKind sets the Kind and the positive Amount of a line: the sign of the file, except on a card line that says
// DEBIT or CREDIT, which decides it.
func withKind(row Row, trnType, kind string) Row {
	row.Kind, row.Amount = BySign(row.Amount)
	if kind == Card && trnType == "DEBIT" {
		row.Kind = "OUTCOME"
	} else if kind == Card && trnType == "CREDIT" {
		row.Kind = "INCOME"
	}

	return row
}

// BySign is the Kind of a signed amount, INCOME when positive, with the amount made positive.
func BySign(amount int64) (string, int64) {
	if amount > 0 {
		return "INCOME", amount
	}

	return "OUTCOME", -amount
}

// decode turns the file into UTF-8, reading it as Windows-1252 (a superset of Latin-1, what CHARSET:1252 and
// ISO-8859-1 files hold) when it is not valid UTF-8.
func decode(content []byte) string {
	if utf8.Valid(content) {
		return string(content)
	}

	text, _ := charmap.Windows1252.NewDecoder().String(string(content))

	return text
}

// parseCents reads a signed decimal amount, as "-1234.56" or "-1234,56", as cents.
func parseCents(value string) (int64, error) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64)
	if err != nil {
		return 0, err
	}

	return int64(math.Round(f * 100)), nil
}

// nubank is the institution of the CSVs it exports.
const nubank = "Nubank"

// CurrencyBRL is the only currency of the statements and Connections for now.
const CurrencyBRL = "BRL"

// parseNubankCSV reads the Nubank card statement (date,title,amount) or account statement
// (Data,Valor,Identificador,Description), told apart by the header. Amounts are BRL.
func parseNubankCSV(content []byte) ([]Statement, error) {
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(decode(content), "\ufeff"))).ReadAll()
	if err != nil || len(records) == 0 {
		return nil, ErrUnrecognized
	}

	statement := Statement{Institution: nubank, Currency: CurrencyBRL}
	layout, sign, width := time.DateOnly, int64(-1), 3
	switch strings.Join(records[0], ",") {
	case "date,title,amount":
		statement.Account, statement.Kind = "Cartão", Card
		sign = 1
	case "Data,Valor,Identificador,Descrição":
		statement.Account, statement.Kind = "Conta", Checking
		layout, width = "02/01/2006", 4
	default:
		return nil, ErrUnrecognized
	}

	seen := map[string]int{}
	for _, record := range records[1:] {
		if len(record) != width {
			return nil, ErrUnrecognized
		}

		var row Row
		if statement.Kind == Card {
			row = Row{Date: record[0], Description: record[1]}
			row.Amount, err = parseCents(record[2])
		} else {
			row = Row{Date: record[0], ID: record[2], Description: record[3]}
			row.Amount, err = parseCents(record[1])
		}

		date, dateErr := time.Parse(layout, row.Date)
		if err != nil || dateErr != nil {
			return nil, ErrUnrecognized
		}

		row.Date = date.Format(time.DateOnly)

		if statement.Kind == Card {
			key := fmt.Sprintf("%s|%s|%d", row.Date, row.Description, row.Amount)
			row.ID = fmt.Sprintf("%s|%d", key, seen[key])
			seen[key]++
		}

		row.Kind, row.Amount = BySign(-row.Amount * sign)
		statement.Rows = append(statement.Rows, row)
	}

	return []Statement{statement}, nil
}

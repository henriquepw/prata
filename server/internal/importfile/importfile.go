// Package importfile reads the bank files the user imports into Movimentos.
package importfile

import (
	"bytes"
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
)

// ErrUnrecognized means the file is not in a format this package reads.
var ErrUnrecognized = errors.New("unrecognized file")

// Bank account kinds, as the schema spells them.
const (
	Checking = "CHECKING"
	Card     = "CARD"
)

// Statement is the lines of one Conta bancária declared by the file, with the Moeda it declares, empty when none.
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
	if statements, err := ParseOFX(content); err == nil {
		return statements, nil
	}

	return ParseNubankCSV(content)
}

// line is a Row as read, with the TRNTYPE the file gives it.
type line struct {
	Row
	Type string
}

// tag matches an OFX tag with the text after it, which is the whole value in SGML (1.x) and in XML (2.x) alike.
var tag = regexp.MustCompile(`<([A-Za-z0-9.]+)>\s*([^<]*)`)

// ParseOFX reads an OFX 1.x or 2.x file of bank accounts and credit cards, in the encoding it declares.
func ParseOFX(content []byte) ([]Statement, error) {
	text := decode(content)
	if !strings.Contains(strings.ToUpper(text), "<OFX>") {
		return nil, ErrUnrecognized
	}

	var statements []Statement
	var row *line
	var rows []line
	flush := func() {
		if len(statements) == 0 {
			return
		}

		last := &statements[len(statements)-1]
		for _, r := range rows {
			last.Rows = append(last.Rows, finish(r.Row, r.Type, last.Kind))
		}
		rows = nil
	}

	for _, m := range tag.FindAllStringSubmatch(text, -1) {
		name, value := strings.ToUpper(m[1]), html.UnescapeString(strings.TrimSpace(m[2]))
		switch name {
		case "STMTRS", "CCSTMTRS":
			flush()
			row = nil
			statements = append(statements, Statement{})
		case "BANKACCTFROM":
			statements[len(statements)-1].Kind = Checking
		case "CCACCTFROM":
			statements[len(statements)-1].Kind = Card
		case "STMTTRN":
			rows = append(rows, line{})
			row = &rows[len(rows)-1]
		}

		if len(statements) == 0 {
			continue
		}

		current := &statements[len(statements)-1]
		switch name {
		case "BANKID":
			current.Institution = value
		case "ORG":
			if current.Institution == "" {
				current.Institution = value
			}
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

	for _, statement := range statements {
		for _, row := range statement.Rows {
			if row.ID == "" || row.Date == "" {
				return nil, ErrUnrecognized
			}
		}
	}

	return statements, nil
}

// finish sets the Kind and the positive Amount of a line: the sign of the file, except on a card line that says
// DEBIT or CREDIT, which decides it.
func finish(row Row, trnType, kind string) Row {
	row.Kind = "OUTCOME"
	if row.Amount > 0 {
		row.Kind = "INCOME"
	}

	if kind == Card && trnType == "DEBIT" {
		row.Kind = "OUTCOME"
	} else if kind == Card && trnType == "CREDIT" {
		row.Kind = "INCOME"
	}

	if row.Amount < 0 {
		row.Amount = -row.Amount
	}

	return row
}

// decode turns the file into UTF-8: Latin-1 when the header declares CHARSET 1252 or ISO-8859-1, or when the bytes
// are not valid UTF-8.
func decode(content []byte) string {
	header, _, _ := bytes.Cut(content, []byte("<"))
	declared := strings.ToUpper(string(header))
	if utf8.Valid(content) && !strings.Contains(declared, "1252") && !strings.Contains(declared, "8859") {
		return string(content)
	}

	runes := make([]rune, len(content))
	for i, b := range content {
		runes[i] = rune(b)
	}

	return string(runes)
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

// ParseNubankCSV reads the Nubank card statement (date,title,amount) or account statement
// (Data,Valor,Identificador,Descrição), told apart by the header. Amounts are BRL.
func ParseNubankCSV(content []byte) ([]Statement, error) {
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(decode(content), "\ufeff"))).ReadAll()
	if err != nil || len(records) == 0 {
		return nil, ErrUnrecognized
	}

	statement := Statement{Institution: nubank, Currency: "BRL"}
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

		row.Kind, row.Amount = "OUTCOME", row.Amount*sign
		if row.Amount < 0 {
			row.Kind, row.Amount = "INCOME", -row.Amount
		}

		statement.Rows = append(statement.Rows, row)
	}

	return []Statement{statement}, nil
}

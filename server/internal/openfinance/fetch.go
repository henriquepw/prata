// Package openfinance fetches the movements of the users' Connections from Pluggy.
package openfinance

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/pkg/id"
	"github.com/henriquepw/prata/server/pkg/importfile"
	"github.com/henriquepw/prata/server/pkg/pluggy"
)

// overlap is how far before the last fetch a later fetch reads again, since Pluggy can post a line dated in the
// past; the unique external id keeps the lines it already gave from becoming second Movements.
const overlap = 7 * 24 * time.Hour

// cardPayment is the operationType of the line that pays the card bill, which the bank account paying it shows too.
const cardPayment = "PAGAMENTO_FATURA"

// Config is the instance's Open Finance setup: Key encrypts the users' Pluggy credentials, and an empty Key turns
// the feature off.
type Config struct {
	Key    string
	Pluggy pluggy.Client
}

// Enabled reports whether Open Finance is on, that is, whether the instance has a Key.
func (c Config) Enabled() bool {
	return c.Key != ""
}

// Fetcher reads the Connections from Pluggy with the owners' saved credentials.
type Fetcher struct {
	DB *store.Store
	Config
}

// Fetch stores the posted lines of every bank account of the user's Connections as pending Movements, or of every
// Connection when userID is empty. A Connection that fails does not stop the others: the error is returned, joined, after
// all were tried. It never makes a domain op.
func (f Fetcher) Fetch(ctx context.Context, userID id.ID, now time.Time) error {
	targets, err := f.DB.FetchTargets(ctx, userID)
	if err != nil {
		return err
	}

	var errs []error
	for _, target := range targets {
		if err := f.fetch(ctx, target, now); err != nil {
			errs = append(errs, fmt.Errorf("connection %s: %w", target.ID, err))
		}
	}

	return errors.Join(errs...)
}

func (f Fetcher) fetch(ctx context.Context, target store.FetchTarget, now time.Time) error {
	credentials, err := f.DB.PluggyCredentials(ctx, f.Key, target.UserID)
	if err != nil {
		return fmt.Errorf("reading credentials: %w", err)
	}

	apiKey, err := f.Pluggy.Authenticate(ctx, credentials.ClientID, credentials.ClientSecret)
	if err != nil {
		return err
	}

	item, err := f.Pluggy.Item(ctx, apiKey, target.ItemID)
	if err != nil {
		return err
	}

	if err := f.DB.UpdateConnectionItem(ctx, target.ID, item); err != nil {
		return err
	}

	accounts, err := f.Pluggy.Accounts(ctx, apiKey, target.ItemID)
	if err != nil {
		return err
	}

	dateFrom := dateFrom(target, now)

	var statements []importfile.Statement
	for _, account := range accounts {
		kind, ok := bankAccountKinds[account.Type]
		if !ok || account.Currency != importfile.CurrencyBRL {
			continue
		}

		transactions, err := f.Pluggy.Transactions(ctx, apiKey, account.ID, dateFrom)
		if err != nil {
			return err
		}

		statements = append(statements, importfile.Statement{Institution: target.Institution, Account: account.Name, Kind: kind, Rows: rows(transactions)})
	}

	if _, _, err := f.DB.ImportConnectionMovements(ctx, target.UserID, target.AccountID, target.ID, statements); err != nil {
		return err
	}

	return f.DB.MarkFetched(ctx, target.ID, now)
}

var bankAccountKinds = map[string]string{"BANK": importfile.Checking, "CREDIT": importfile.Card}

// dateFrom is the first day to read: the start of the month on the first fetch, then a week before the last one.
func dateFrom(target store.FetchTarget, now time.Time) string {
	last, err := time.Parse(time.RFC3339, target.LastFetchedAt.String)
	if err != nil {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly)
	}

	return last.Add(-overlap).In(now.Location()).Format(time.DateOnly)
}

// rows keeps the posted lines but the card bill payments. The sign of the amount tells the Tipo, whatever the type
// says: a card purchase comes as CREDIT with a negative amount in the sandbox.
func rows(transactions []pluggy.Transaction) []importfile.Row {
	var posted []importfile.Row
	for _, t := range transactions {
		cents, ok := reaisToCents(string(t.Amount))
		if t.Status != "POSTED" || t.OperationType == cardPayment || !ok || cents == 0 || len(t.Date) < len(time.DateOnly) {
			continue
		}

		row := importfile.Row{ID: t.ID, Description: t.Description, Date: t.Date[:len(time.DateOnly)]}
		row.Kind, row.Amount = importfile.BySign(cents)
		posted = append(posted, row)
	}

	return posted
}

// reaisToCents reads an amount in reais, as Pluggy writes it, into signed cents, rounding half away from zero
// without going through a float.
func reaisToCents(amount string) (cents int64, ok bool) {
	r, ok := new(big.Rat).SetString(amount)
	if !ok {
		return 0, false
	}

	sign := int64(r.Sign())
	r.Abs(r).Mul(r, big.NewRat(100, 1))
	rounded := new(big.Int).Quo(new(big.Int).Add(new(big.Int).Lsh(r.Num(), 1), r.Denom()), new(big.Int).Lsh(r.Denom(), 1))

	return sign * rounded.Int64(), true
}

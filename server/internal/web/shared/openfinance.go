package shared

import "github.com/henriquepw/prata/server/internal/pluggy"

// OpenFinance is the instance's Open Finance setup: Key encrypts the users' Pluggy credentials, and an empty Key
// turns the feature off.
type OpenFinance struct {
	Key    string
	Pluggy pluggy.Client
}

// Enabled reports whether Open Finance is on, that is, whether the instance has a Key.
func (o OpenFinance) Enabled() bool {
	return o.Key != ""
}

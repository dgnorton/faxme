package accounts

import (
	"encoding/json"
	"io/ioutil"
)

type Account struct {
	FaxNumber string   `json:"fax_number"`
	Contacts  []string `json:"contacts"`
}

type Accounts struct {
	acnts map[string]*Account
}

func NewAccounts() *Accounts {
	return &Accounts{
		acnts: map[string]*Account{},
	}
}

func ReadFile(path string) (*Accounts, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	a := []*Account{}

	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}

	acnts := NewAccounts()
	for _, acnt := range a {
		acnts.acnts[acnt.FaxNumber] = acnt
	}

	return acnts, nil
}

func (a *Accounts) Find(fax string) *Account {
	return a.acnts[fax]
}

func (a *Accounts) Add(fax string, contacts []string) {
	a.acnts[fax] = &Account{
		FaxNumber: fax,
		Contacts:  contacts,
	}
}

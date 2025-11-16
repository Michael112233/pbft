package core

import "math/big"

type Account struct {
	Balance *big.Int
}

func NewAccount() *Account {
	return &Account{
		Balance: big.NewInt(999999999999999999),
	}
}

func (a *Account) Deposit(amount int64) {
	a.Balance.Add(a.Balance, big.NewInt(amount))
}

func (a *Account) Withdraw(amount int64) {
	a.Balance.Sub(a.Balance, big.NewInt(amount))
}

func (a *Account) GetBalance() *big.Int {
	return a.Balance
}

func (a *Account) SetBalance(balance *big.Int) {
	a.Balance = balance
}

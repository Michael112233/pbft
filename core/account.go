package core

type Account struct {
	Balance int64
}

func NewAccount() *Account {
	return &Account{
		Balance: 999999999,
	}
}

func (a *Account) Deposit(amount int64) {
	a.Balance += amount
}

func (a *Account) Withdraw(amount int64) {
	a.Balance -= amount
}

func (a *Account) GetBalance() int64 {
	return a.Balance
}

func (a *Account) SetBalance(balance int64) {
	a.Balance = balance
}

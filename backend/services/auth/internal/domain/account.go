package domain

type Account struct {
	ID           int64
	Email        string
	Username     string
	PasswordHash string
}

type Event struct {
	ID      int64
	Topic   string
	Key     string
	Payload []byte
}

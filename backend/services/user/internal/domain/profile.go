package domain

type Profile struct {
	ID            int64
	Username      string
	Discriminator string
	DisplayName   string
	AvatarURL     string
	StatusText    string
}

type Event struct {
	ID           int64
	Topic        string
	Key          string
	Payload      []byte
	RecipientIDs []string
}

const (
	RelPendingOut = "pending_out"
	RelPendingIn  = "pending_in"
	RelFriend     = "friend"
	RelBlocked    = "blocked"
)

const Topic = "user.events"

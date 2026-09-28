package domain

const (
	KindDM    = "dm"
	KindGroup = "group"
	KindGuild = "guild"
	Topic     = "message.events"
	MaxGroup  = 10
	MaxLen    = 4000
)

type DMChannel struct {
	ID      int64
	Kind    string
	Members []int64
}

type Attachment struct {
	ID          int64
	MessageID   int64
	MediaID     string
	Filename    string
	ContentType string
	Size        int64
}

type Message struct {
	ID          int64
	ChannelID   int64
	ChannelKind string
	AuthorID    int64
	Content     string
	CreatedAt   int64
	EditedAt    int64
	Attachments []Attachment
}

type Event struct {
	ID      int64
	Topic   string
	Key     string
	Payload []byte
}

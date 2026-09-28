package eventx

import (
	"encoding/json"
	"time"
)

type Envelope struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	OccurredAt   time.Time       `json:"occurred_at"`
	RecipientIDs []string        `json:"recipient_ids,omitempty"`
	GuildID      string          `json:"guild_id,omitempty"`
	ChannelID    string          `json:"channel_id,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

func New(id, typ string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		ID:         id,
		Type:       typ,
		OccurredAt: time.Now().UTC(),
		Payload:    raw,
	}, nil
}

func (e Envelope) Bytes() ([]byte, error) {
	return json.Marshal(e)
}

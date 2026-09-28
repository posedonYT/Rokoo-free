package dispatch

import (
	"testing"

	"rokko.dev/pkg/eventx"
)

func TestRecipientsPreferExplicit(t *testing.T) {
	got := Recipients(eventx.Envelope{Type: "friend.requested", RecipientIDs: []string{"2"}, GuildID: "9"}, func(string) []string {
		return []string{"nope"}
	}, nil)
	if len(got) != 1 || got[0] != "2" {
		t.Fatalf("%v", got)
	}
}

func TestRecipientsGuildDedup(t *testing.T) {
	got := Recipients(eventx.Envelope{Type: "message.created", GuildID: "9"}, func(id string) []string {
		if id == "9" {
			return []string{"1", "1", "2"}
		}
		return nil
	}, nil)
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

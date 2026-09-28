package domain

import "time"

const (
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleMember   = "member"
	ChannelText  = "text"
	ChannelVoice = "voice"
	Topic        = "guild.events"
)

type Guild struct {
	ID      int64
	Name    string
	IconURL string
	OwnerID int64
	MyRole  string
}

type Channel struct {
	ID       int64
	GuildID  int64
	Name     string
	Type     string
	Position int
}

type Member struct {
	GuildID int64
	UserID  int64
	Role    string
}

type Invite struct {
	Code      string
	GuildID   int64
	CreatorID int64
	MaxUses   int
	Uses      int
	ExpiresAt *time.Time
}

type Event struct {
	ID      int64
	Topic   string
	Key     string
	Payload []byte
}

func CanManageGuild(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

func CanDeleteGuild(role string) bool { return role == RoleOwner }

func CanManageChannels(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

func CanKick(actorRole, targetRole string) bool {
	if targetRole == RoleOwner || actorRole == targetRole && actorRole == RoleOwner {
		return false
	}
	if actorRole == RoleOwner && targetRole != RoleOwner {
		return true
	}
	return actorRole == RoleAdmin && targetRole == RoleMember
}

func CanSetRole(actorRole, targetRole, newRole string) bool {
	if actorRole != RoleOwner || targetRole == RoleOwner {
		return false
	}
	return newRole == RoleAdmin || newRole == RoleMember
}

func CanModerate(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

func ValidChannelType(t string) bool {
	return t == ChannelText || t == ChannelVoice
}

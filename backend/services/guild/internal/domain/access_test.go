package domain

import "testing"

func TestKickAndRoles(t *testing.T) {
	if CanKick(RoleOwner, RoleOwner) || !CanKick(RoleOwner, RoleAdmin) || !CanKick(RoleAdmin, RoleMember) || CanKick(RoleAdmin, RoleAdmin) || CanKick(RoleMember, RoleMember) {
		t.Fatal("kick matrix")
	}
	if !CanSetRole(RoleOwner, RoleMember, RoleAdmin) || CanSetRole(RoleAdmin, RoleMember, RoleAdmin) || CanSetRole(RoleOwner, RoleOwner, RoleAdmin) || CanSetRole(RoleOwner, RoleMember, RoleOwner) {
		t.Fatal("role matrix")
	}
	if !CanModerate(RoleAdmin) || CanModerate(RoleMember) || !CanDeleteGuild(RoleOwner) || CanDeleteGuild(RoleAdmin) {
		t.Fatal("moderation matrix")
	}
}

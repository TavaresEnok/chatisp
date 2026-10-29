package auth

// Action is one panel operation authorized by the fixed role matrix of section 12.
// Administrator: provider settings, users, sessions and MFA reset.
// Supervisor and agent: own session only. They do not administer users,
// numbers or integrations. Sector, report and canned-response operations
// arrive with those modules and must be added here, not as a permission editor.
type Action string

const (
	ActionSessionRead        Action = "session.read"
	ActionSessionEnd         Action = "session.end"
	ActionSessionRevokeOwn   Action = "sessions.revoke_own"
	ActionSessionRevokeOther Action = "sessions.revoke_other"
	ActionSessionList        Action = "sessions.list"
	ActionInvite             Action = "users.invite"
	ActionMembershipRead     Action = "users.read"
	ActionMembershipDisable  Action = "users.disable"
	ActionMembershipEnable   Action = "users.enable"
	ActionMFAEnroll          Action = "mfa.enroll"
	ActionMFAReset           Action = "mfa.reset"
)

// RoleAllows reports whether a tenant role may perform the action.
// Platform provider administration is not a tenant role; callers check IsSuperadmin.
func RoleAllows(role string, action Action) bool {
	switch action {
	case ActionSessionRead, ActionSessionEnd, ActionSessionRevokeOwn, ActionMFAEnroll:
		return role == "admin" || role == "supervisor" || role == "agent"
	case ActionInvite, ActionMembershipRead, ActionMembershipDisable, ActionMembershipEnable,
		ActionSessionList, ActionSessionRevokeOther, ActionMFAReset:
		return role == "admin"
	default:
		return false
	}
}

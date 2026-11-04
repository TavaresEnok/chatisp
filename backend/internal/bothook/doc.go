// Package bothook is the external bot hook for one inbox.
// The bot is an in-process fake. This package does not dial the network.
// Possession stays on conversations.controller and controller_epoch.
// A human queue row is controller menu with no assignee: the database
// requires an assignee whenever the controller is human.
package bothook

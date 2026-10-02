// Package intents decides what skua identifies with, from what the
// Developer Portal has actually granted rather than from what a module would
// like.
//
// Asking for a privileged intent the portal has not switched on gets close
// code 4014, and the gateway library reconnect-loops on it while the process
// looks healthy, so catching it needs a ready-watchdog. skua never asks: it reads the application's flags first and identifies with the
// intersection, so the failure cannot happen.
package intents

import (
	"slices"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
)

// Want is what one module asks for. A missing Required intent skips the
// module; a missing Optional one only narrows it.
type Want struct {
	Required, Optional gateway.Intents
}

// Granted returns every intent skua may identify with: all non-privileged
// ones, plus each privileged one whose portal toggle is on. The _LIMITED
// flags are what a bot under 100 servers gets for the same toggle, so they
// count.
func Granted(flags discord.ApplicationFlags) gateway.Intents {
	g := gateway.IntentsNonPrivileged
	if flags&(discord.ApplicationFlagGatewayPresence|discord.ApplicationFlagGatewayPresenceLimited) != 0 {
		g |= gateway.IntentGuildPresences
	}
	if flags&(discord.ApplicationFlagGatewayGuildMembers|discord.ApplicationFlagGatewayGuildMemberLimited) != 0 {
		g |= gateway.IntentGuildMembers
	}
	if flags&(discord.ApplicationFlagGatewayMessageContent|discord.ApplicationFlagGatewayMessageContentLimited) != 0 {
		g |= gateway.IntentMessageContent
	}
	return g
}

// Resolve returns the intents to identify with and the names of the modules
// that cannot run under granted, sorted.
func Resolve(wants map[string]Want, granted gateway.Intents) (identify gateway.Intents, skipped []string) {
	for name, w := range wants {
		if w.Required&^granted != 0 {
			skipped = append(skipped, name)
			continue
		}
		identify |= w.Required | (w.Optional & granted)
	}
	slices.Sort(skipped)
	return identify, skipped
}

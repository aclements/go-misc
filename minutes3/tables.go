// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
)

var whoMap = map[string]string{
	"andybons":  "andybons",
	"bradfitz":  "bradfitz",
	"gri":       "griesemer",
	"iant":      "ianlancetaylor",
	"r":         "robpike",
	"rsc":       "rsc",
	"sfrancia":  "spf13",
	"austin":    "aclements",
	"julieqiu":  "julieqiu",
	"adonovan":  "adonovan",
	"bracewell": "rolandshoemaker",
	"roland":    "rolandshoemaker",
	"cherryyz":  "cherrymui",
	"cherry":    "cherrymui",
}

func gitWho(who string) string {
	if whoMap[who] != "" {
		return "@" + whoMap[who]
	}
	fmt.Fprintf(os.Stderr, "warning: unknown attendee %s; assuming GitHub @%s\n", who, who)
	return "@" + who
}

var actionMap = map[string]string{
	"accepted":       "no change in consensus; **accepted** 🎉",
	"declined":       "no change in consensus; **declined**",
	"retracted":      "proposal retracted by author; **declined**",
	"hold":           "put on hold",
	"on hold":        "put on hold",
	"unhold":         "taken off hold",
	"likely accept":  "**likely accept**; last call for comments ⏳",
	"likely decline": "**likely decline**; last call for comments ⏳",
	"discuss":        "discussion ongoing",
	"add":            "added to minutes",
	"removed":        "removed from proposal process",
	"comment":        "commented",
	"infeasible":     "declined as infeasible",
}

// There's also "check", which is mapped to "comment" plus posting the proposal
// details.

func updateMsg(old, new, reason string) string {
	msg := updateMsgs[reason]
	if msg == "" {
		return updateMsgs[new]
	}
	return msg + msgFooter + "\n"
}

const msgFooter = "— aclements for the proposal review group"

var updateMsgs = map[string]string{
	"duplicate": `
This proposal is a duplicate of a previously discussed proposal, as noted above,
and there is no significant new information to justify reopening the discussion.
The issue has therefore been **[declined as a duplicate](https://go.dev/s/proposal-status#declined-as-duplicate)**.
`,
	"retracted": `
This proposal has been **[declined as retracted](https://go.dev/s/proposal-status#declined-as-retracted)**.
`,
	"infeasible": `
This proposal has been **[declined as infeasible](https://go.dev/s/proposal-status#declined-as-infeasible)**.
`,
	"obsolete": `
This proposal has been **[declined as obsolete](https://go.dev/s/proposal-status#declined-as-obsolete)**.
`,
	"Active": `
This proposal has been added to the [active column](https://go.dev/s/proposal-status#active) of the proposals project
and will now be reviewed at the weekly proposal review meetings.
`,
	"Likely Accept": `
Based on the discussion above, this proposal seems like a **[likely accept](https://go.dev/s/proposal-status#likely-accept)**.
`,
	"Likely Decline": `
Based on the discussion above, this proposal seems like a **[likely decline](https://go.dev/s/proposal-status#likely-decline)**.
`,
	"Accepted": `
No change in consensus, so **[accepted](https://go.dev/s/proposal-status#accepted)**. 🎉
This issue now tracks the work of implementing the proposal.
`,
	"Declined": `
No change in consensus, so **[declined](https://go.dev/s/proposal-status#declined)**.
`,
	"Hold": `
**[Placed on hold](https://go.dev/s/proposal-status#hold)**.
`,
	"removed": `
**Removed from the [proposal process](https://go.dev/s/proposal)**.
This was determined not to be a “significant change to the language, libraries, or tools”
or otherwise of significant importance or interest to the broader Go community.
`,
}

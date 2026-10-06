// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nbd-wtf/go-nostr"
)

// The Control Plane (CORD-02 §5, CORD-04) is one stream per community of
// versioned editions, each a kind 3308 rumor in a plaintext seal signed by
// its real author. The fold replays them into current state: the
// owner-rooted roster first, then every entity gated on it. This is a port
// of control.ts, edition.ts and version.ts, cut to what the bridge reads:
// the roster, the channels and the banlist. Metadata, invite registries and
// pins are left out, which changes nothing above: none of them feeds an
// authority decision.

// Permission bits (roles.ts).
const (
	PermManageRoles    uint64 = 1 << 0
	PermManageChannels uint64 = 1 << 1
	PermBan            uint64 = 1 << 4
	PermManageMessages uint64 = 1 << 5
)

const (
	vskRole    = "1"
	vskChannel = "2"
	vskGrant   = "3"
	vskBanlist = "4"

	nameMaxBytes        = 64
	maxRolesPerMember   = 64
	maxRolesPerCommunty = 100
	// noRank is Number.MAX_SAFE_INTEGER, the rank of a member with no role.
	noRank = 9007199254740991
)

// Role is a folded role. Position is a float64 because it is a JS number on
// every other client, and ranks must compare the same way.
type Role struct {
	ID       string
	Position float64
	Perms    uint64
}

// Grant is the roles one member holds.
type Grant struct {
	Member  string
	RoleIDs []string
}

// Roster is the folded roles and grants.
type Roster struct {
	Roles  []Role
	Grants []Grant
}

func (r *Roster) role(id string) (Role, bool) {
	for _, x := range r.Roles {
		if x.ID == id {
			return x, true
		}
	}
	return Role{}, false
}

func (r *Roster) rolesOf(member string) []Role {
	var out []Role
	for _, g := range r.Grants {
		if g.Member != member {
			continue
		}
		for _, id := range g.RoleIDs {
			if x, ok := r.role(id); ok {
				out = append(out, x)
			}
		}
	}
	return out
}

func (r *Roster) has(member string, bits uint64) bool {
	var perms uint64
	for _, x := range r.rolesOf(member) {
		perms |= x.Perms
	}
	return perms&bits == bits
}

// highest is the member's best (lowest) position, if they hold any role.
func (r *Roster) highest(member string) (float64, bool) {
	best, ok := 0.0, false
	for _, x := range r.rolesOf(member) {
		if !ok || x.Position < best {
			best, ok = x.Position, true
		}
	}
	return best, ok
}

func (r *Roster) authorized(actor, owner string, bits uint64) bool {
	return actor == owner || r.has(actor, bits)
}

func (r *Roster) outranks(actor, owner string, target float64) bool {
	if actor == owner {
		return true
	}
	p, ok := r.highest(actor)
	return ok && p < target
}

func (r *Roster) canActOnPosition(actor, owner string, target float64, bits uint64) bool {
	return actor == owner || (r.has(actor, bits) && r.outranks(actor, owner, target))
}

// canActOnMember also refuses the owner as a target.
func (r *Roster) canActOnMember(actor, owner, target string, bits uint64) bool {
	if target == owner {
		return false
	}
	pos, ok := r.highest(target)
	if !ok {
		pos = noRank
	}
	return r.canActOnPosition(actor, owner, pos, bits)
}

// Folded is the Control Plane replayed into current state.
type Folded struct {
	Roster   Roster
	Owner    string
	Channels map[string]Channel // by id hex, deleted ones included
	Banned   map[string]bool
}

// Channel is a channel's folded definition.
type Channel struct {
	ID      string
	Name    string
	Private bool
	Deleted bool
}

// IsBanned is whether the banlist names pk.
func (f *Folded) IsBanned(pk string) bool { return f != nil && f.Banned[pk] }

// IsModerator is whether pk may delete other members' messages.
func (f *Folded) IsModerator(pk string) bool {
	return f != nil && f.Roster.authorized(pk, f.Owner, PermManageMessages)
}

// ── editions ───────────────────────────────────────────────────────────────

type citation struct {
	entity  [32]byte
	version uint64
	hash    [32]byte
}

type edition struct {
	author    string
	vsk       string
	entity    [32]byte
	version   uint64
	prev      *[32]byte
	content   string
	self      [32]byte
	createdAt int64
	rumorID   [32]byte
	rumorHex  string
	authority *citation
}

// decimal is CORD-01 §5's tag number: decimal, no leading zeros. A peer
// that parsed "04" or "+4" would honor an event another drops, and nobody
// would ever see why.
var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func parseDecimal(s string) (uint64, bool) {
	if !decimal.MatchString(s) {
		return 0, false
	}
	// A version past u64 cannot be hashed (TS's setBigUint64 throws too).
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func citationOf(tags [][]string) *citation {
	for _, t := range tags {
		if len(t) < 4 || t[0] != "vac" {
			continue
		}
		e, err1 := hex32(t[1])
		h, err2 := hex32(t[3])
		v, ok := parseDecimal(t[2])
		if err1 != nil || err2 != nil || !ok {
			return nil
		}
		return &citation{entity: e, version: v, hash: h}
	}
	return nil
}

// editionLabel is frozen by CORD-04 §1; renaming it would re-hash every chain.
const editionLabel = "vector-community/v1/edition"

// editionHash is sha256 of len64(label) ‖ label ‖ entity ‖ version ‖
// has_prev ‖ prev or zeros ‖ len64(content) ‖ content, the content as the
// exact bytes on the wire.
func editionHash(entity [32]byte, version uint64, prev *[32]byte, content string) [32]byte {
	h := sha256.New()
	_ = binary.Write(h, binary.BigEndian, uint64(len(editionLabel)))
	h.Write([]byte(editionLabel))
	h.Write(entity[:])
	_ = binary.Write(h, binary.BigEndian, version)
	if prev != nil {
		h.Write([]byte{1})
		h.Write(prev[:])
	} else {
		h.Write([]byte{0})
		h.Write(zero32[:])
	}
	_ = binary.Write(h, binary.BigEndian, uint64(len(content)))
	h.Write([]byte(content))
	return [32]byte(h.Sum(nil))
}

// parseEdition extracts the edition machinery from an opened control event.
// The stream layer already proved authorship and the rumor's integrity.
// Duplicate machinery tags are refused: they make the canonical bytes
// ambiguous. Roster authorization is the fold's job, not this one's.
func parseEdition(o *Opened) (*edition, bool) {
	// Control seals must be plaintext (CORD-02 §5): an encrypted one could
	// never survive a compaction re-wrap, so honoring it would mint state
	// that silently vanishes for the next joiner.
	if o.Kind != KindControl || o.SealKind != KindSealPlaintext {
		return nil, false
	}
	for _, name := range []string{"vsk", "eid", "ev", "ep", "vac"} {
		n := 0
		for _, t := range o.Tags {
			if len(t) > 0 && t[0] == name {
				n++
			}
		}
		if n > 1 {
			return nil, false
		}
	}
	// get is TS's tags.find(...)?.[1]: ok only when the first such tag
	// carries a value, so a bare ["ep"] means no prev, as it does upstream.
	get := func(name string) (string, bool) {
		for _, t := range o.Tags {
			if len(t) > 0 && t[0] == name {
				if len(t) < 2 {
					return "", false
				}
				return t[1], true
			}
		}
		return "", false
	}
	vsk, ok := get("vsk")
	if !ok {
		return nil, false
	}
	eidHex, _ := get("eid")
	eid, err := hex32(eidHex)
	if err != nil {
		return nil, false
	}
	evStr, ok := get("ev")
	if !ok {
		return nil, false
	}
	version, ok := parseDecimal(evStr)
	if !ok {
		return nil, false
	}
	var prev *[32]byte
	if ep, ok := get("ep"); ok {
		p, err := hex32(ep)
		if err != nil {
			return nil, false
		}
		prev = &p
	}
	rid, err := hex32(o.RumorID)
	if err != nil {
		return nil, false
	}
	return &edition{
		author: o.Author, vsk: vsk, entity: eid, version: version, prev: prev, content: o.Content,
		self: editionHash(eid, version, prev, o.Content), createdAt: o.CreatedAt,
		rumorID: rid, rumorHex: strings.ToLower(o.RumorID), authority: citationOf(o.Tags),
	}, true
}

// openControl opens every control wrap that decodes under s into editions.
func openControl(wraps []*nostr.Event, s StreamKey) []*edition {
	var out []*edition
	for _, w := range wraps {
		if w.PubKey != s.PK {
			continue
		}
		o, err := Open(w, s)
		if err != nil {
			continue
		}
		if e, ok := parseEdition(o); ok {
			out = append(out, e)
		}
	}
	return out
}

// ── version chains (version.ts) ───────────────────────────────────────────

// chainHead folds one entity's editions into its head, chain-checked, with
// nothing held yet. Every sibling per version is carried, not a single
// winner: a tiebreak id is a hash of content the publisher picks, so anyone
// able to publish could grind a junk edition that sorts first and pin the
// entity. The link decides which sibling is real, and a forgery cannot fake
// prev continuity to an edition it doesn't have.
func chainHead(eds []*edition) (head int, ok bool) {
	byVersion := map[uint64][]int{}
	for i, e := range eds {
		byVersion[e.version] = append(byVersion[e.version], i)
	}
	if len(byVersion) == 0 {
		return 0, false
	}
	for _, l := range byVersion {
		slices.SortStableFunc(l, func(x, y int) int { return bytes.Compare(eds[x].rumorID[:], eds[y].rumorID[:]) })
	}
	versions := make([]uint64, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	slices.Sort(versions)
	base := byVersion[versions[0]]
	head = base[0]
	if versions[0] == 1 {
		for _, i := range base {
			if eds[i].prev == nil {
				head = i
				break
			}
		}
	}
	for k := 0; k+1 < len(versions); k++ {
		if versions[k+1] != versions[k]+1 {
			break
		}
		next := -1
		for _, i := range byVersion[versions[k+1]] {
			if eds[i].prev != nil && *eds[i].prev == eds[head].self {
				next = i
				break
			}
		}
		if next < 0 {
			break
		}
		head = next
	}
	return head, true
}

// candidates orders an entity's editions for pickHead: the chain head
// first, then the rest by version descending and rumor id ascending, each
// rumor once.
func candidates(eds []*edition) []*edition {
	var out []*edition
	seen := map[string]bool{}
	if h, ok := chainHead(eds); ok {
		out = append(out, eds[h])
		seen[eds[h].rumorHex] = true
	}
	var rest []*edition
	for _, e := range eds {
		if seen[e.rumorHex] {
			continue
		}
		seen[e.rumorHex] = true
		rest = append(rest, e)
	}
	slices.SortStableFunc(rest, func(a, b *edition) int {
		if a.version != b.version {
			if a.version > b.version {
				return -1
			}
			return 1
		}
		return strings.Compare(a.rumorHex, b.rumorHex)
	})
	return append(out, rest...)
}

type head struct {
	version uint64
	hash    [32]byte
}

// pickHead is the first candidate the gate admits, swapped only for a
// same-version sibling by a higher-ranked author.
func pickHead(cands []*edition, heads map[string]head, gate func(*edition) bool, rank func(string) float64) *edition {
	var h *edition
	for _, p := range cands {
		if !gate(p) {
			continue
		}
		if h == nil {
			h = p
			continue
		}
		if p.version != h.version {
			break
		}
		if rank(p.author) < rank(h.author) {
			h = p
		}
	}
	if h != nil {
		heads[hexOf(h.entity)] = head{h.version, h.self}
	}
	return h
}

// versionGroups splits candidates into runs of equal version, ascending.
func versionGroups[T any](cands []T, version func(T) uint64) [][]T {
	sorted := slices.Clone(cands)
	slices.SortStableFunc(sorted, func(a, b T) int {
		va, vb := version(a), version(b)
		switch {
		case va < vb:
			return -1
		case va > vb:
			return 1
		}
		return 0
	})
	var groups [][]T
	for _, c := range sorted {
		if n := len(groups); n > 0 && version(groups[n-1][0]) == version(c) {
			groups[n-1] = append(groups[n-1], c)
		} else {
			groups = append(groups, []T{c})
		}
	}
	return groups
}

// citationSatisfied answers "have I synced enough of this actor's Grant to
// judge them" (CORD-04 §5), never "may they act": the caller still checks
// the current roster. It must name the actor's own Grant: citing a foreign
// edition we happen to hold cannot borrow completeness. Behind the cited
// version, the action parks and self-heals when the Grant arrives.
func citationSatisfied(heads map[string]head, community [32]byte, owner, actor string, c *citation) bool {
	if actor == owner {
		return true
	}
	if c == nil {
		return false
	}
	member, err := hex32(actor)
	if err != nil {
		return false
	}
	eid := grantLocator(community, member)
	if c.entity != eid {
		return false
	}
	h, ok := heads[hexOf(eid)]
	switch {
	case !ok:
		return false
	case h.version > c.version:
		return true
	case h.version == c.version:
		return h.hash == c.hash
	}
	return false
}

// ── roles and grants (roles.ts) ───────────────────────────────────────────

func roleFrom(content string) (Role, bool) {
	var w map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &w) != nil || w == nil {
		return Role{}, false
	}
	var id string
	if json.Unmarshal(w["role_id"], &id) != nil || !isHex64(id) {
		return Role{}, false
	}
	perms, ok := permsFrom(w["permissions"])
	if !ok {
		return Role{}, false
	}
	var pos float64
	if json.Unmarshal(w["position"], &pos) != nil || pos != math.Trunc(pos) || pos < 1 {
		return Role{}, false
	}
	var name string
	_ = json.Unmarshal(w["name"], &name) // a missing or odd name reads as ""
	if len(name) > nameMaxBytes {
		return Role{}, false
	}
	return Role{ID: strings.ToLower(id), Position: pos, Perms: perms}, true
}

var digits = regexp.MustCompile(`^\d+$`)

// permsFrom reads permissions as a decimal string or a number, kept to the
// low 64 bits with JS BigInt's two's complement, which is all any bit the
// protocol defines can reach.
func permsFrom(raw json.RawMessage) (uint64, bool) {
	n := new(big.Int)
	var s string
	var f float64
	switch {
	case json.Unmarshal(raw, &s) == nil:
		if !digits.MatchString(s) {
			return 0, false
		}
		n.SetString(s, 10)
	case json.Unmarshal(raw, &f) == nil:
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, false
		}
		big.NewFloat(math.Trunc(f)).Int(n)
	default:
		return 0, false
	}
	return n.And(n, new(big.Int).SetUint64(math.MaxUint64)).Uint64(), true
}

func grantFrom(content string) (Grant, bool) {
	var w map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &w) != nil || w == nil {
		return Grant{}, false
	}
	var member string
	if json.Unmarshal(w["member"], &member) != nil || !isHex64(member) {
		return Grant{}, false
	}
	var raw []json.RawMessage
	_ = json.Unmarshal(w["role_ids"], &raw)
	ids := []string{}
	for _, r := range raw {
		var id string
		if json.Unmarshal(r, &id) == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxRolesPerMember {
		ids = ids[:maxRolesPerMember]
	}
	return Grant{Member: strings.ToLower(member), RoleIDs: ids}, true
}

type roleCand struct {
	role Role
	e    *edition
}

type grantCand struct {
	grant Grant
	e     *edition
}

// delegate resolves the roster as a fixpoint from the owner, whose rank
// comes from the community id itself and never from a fold. An entity
// waits while a candidate's author has a grant still unsettled, so a
// demotion is seen before the demoted act; once nothing moves, roles and
// then ranks are frozen so a cycle can't stall the fold forever.
func delegate(roleCands map[string][]roleCand, grantCands map[string][]grantCand, community [32]byte, owner string, heads map[string]head) Roster {
	var roster Roster
	settledRoles, settledGrants := map[string]bool{}, map[string]bool{}
	roleEids := slices.Sorted(mapsKeys(roleCands))
	grantEids := slices.Sorted(mapsKeys(grantCands))
	grantEidOf := map[string]string{}
	for eid, cs := range grantCands {
		if len(cs) > 0 {
			grantEidOf[cs[0].grant.Member] = eid
		}
	}
	cited := func(e *edition) bool { return citationSatisfied(heads, community, owner, e.author, e.authority) }
	rank := func(a string) float64 {
		if a == owner {
			return -1
		}
		if p, ok := roster.highest(a); ok {
			return p
		}
		return noRank
	}
	authorityFirst := func(a, b *edition) int {
		ra, rb := rank(a.author), rank(b.author)
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
		return strings.Compare(a.rumorHex, b.rumorHex)
	}
	rankPending := func(author, self string) bool {
		if author == owner {
			return false
		}
		eid, ok := grantEidOf[author]
		return ok && eid != self && !settledGrants[eid]
	}
	settle := func(e *edition) { heads[hexOf(e.entity)] = head{e.version, e.self} }

	rolesFrozen, ranksFrozen := false, false
	for changed := true; changed; {
		changed = false
		for _, eid := range roleEids {
			if settledRoles[eid] {
				continue
			}
			cs := roleCands[eid]
			if !ranksFrozen && slices.ContainsFunc(cs, func(c roleCand) bool { return rankPending(c.e.author, "") }) {
				continue
			}
			admissible := map[*edition]bool{}
			var standing *float64
			for _, group := range versionGroups(cs, func(c roleCand) uint64 { return c.e.version }) {
				slices.SortStableFunc(group, func(a, b roleCand) int { return authorityFirst(a.e, b.e) })
				for _, c := range group {
					mintOK := c.e.author == owner || roster.canActOnPosition(c.e.author, owner, c.role.Position, PermManageRoles)
					replaceOK := c.e.author == owner || standing == nil || roster.outranks(c.e.author, owner, *standing)
					if !mintOK || !replaceOK || !cited(c.e) {
						continue
					}
					admissible[c.e] = true
					p := c.role.Position
					standing = &p
					break // one winner per version: a fork sibling can't sidestep it
				}
			}
			i := slices.IndexFunc(cs, func(c roleCand) bool { return admissible[c.e] })
			if i < 0 {
				continue
			}
			roster.Roles = append(roster.Roles, cs[i].role)
			settledRoles[eid] = true
			settle(cs[i].e)
			changed = true
		}
		for _, eid := range grantEids {
			if settledGrants[eid] {
				continue
			}
			cs := grantCands[eid]
			rolePending := func(rid string) bool { _, ok := roleCands[rid]; return ok && !settledRoles[rid] }
			if !rolesFrozen && slices.ContainsFunc(cs, func(c grantCand) bool { return slices.ContainsFunc(c.grant.RoleIDs, rolePending) }) {
				continue
			}
			if !ranksFrozen && slices.ContainsFunc(cs, func(c grantCand) bool { return rankPending(c.e.author, eid) }) {
				continue
			}
			admissible := map[*edition]bool{}
			var standing *float64
			for _, group := range versionGroups(cs, func(c grantCand) uint64 { return c.e.version }) {
				slices.SortStableFunc(group, func(a, b grantCand) int { return authorityFirst(a.e, b.e) })
				for _, c := range group {
					var positions []float64
					for _, rid := range c.grant.RoleIDs {
						if r, ok := roster.role(rid); ok {
							positions = append(positions, r.Position)
						}
					}
					ok := c.e.author == owner
					if !ok && len(positions) == len(c.grant.RoleIDs) && roster.has(c.e.author, PermManageRoles) {
						ok = !slices.ContainsFunc(positions, func(p float64) bool { return !roster.outranks(c.e.author, owner, p) }) &&
							(standing == nil || roster.outranks(c.e.author, owner, *standing))
					}
					if !ok || !cited(c.e) {
						continue
					}
					admissible[c.e] = true
					standing = nil
					if len(positions) > 0 {
						m := slices.Min(positions)
						standing = &m
					}
					break // one winner per version
				}
			}
			i := slices.IndexFunc(cs, func(c grantCand) bool { return admissible[c.e] })
			if i < 0 {
				continue
			}
			roster.Grants = append(roster.Grants, cs[i].grant)
			settledGrants[eid] = true
			settle(cs[i].e)
			changed = true
		}
		if !changed && !rolesFrozen {
			rolesFrozen, changed = true, true
		} else if !changed && !ranksFrozen {
			ranksFrozen, changed = true, true
		}
	}
	if len(roster.Roles) > maxRolesPerCommunty {
		slices.SortFunc(roster.Roles, func(a, b Role) int { return strings.Compare(a.ID, b.ID) })
		roster.Roles = roster.Roles[:maxRolesPerCommunty]
	}
	return roster
}

// ── the fold ──────────────────────────────────────────────────────────────

// Fold replays editions into current state. A banned author's own editions
// are then dropped and the plane folded again, so a banned admin's roles,
// grants and channels fall away with them. The owner is never banned.
func Fold(eds []*edition, community [32]byte, owner string) *Folded {
	first := foldOnce(eds, community, owner)
	delete(first.Banned, owner)
	if len(first.Banned) == 0 || !slices.ContainsFunc(eds, func(e *edition) bool { return first.Banned[e.author] }) {
		return first
	}
	kept := slices.DeleteFunc(slices.Clone(eds), func(e *edition) bool { return first.Banned[e.author] })
	again := foldOnce(kept, community, owner)
	again.Banned = first.Banned
	return again
}

func foldOnce(eds []*edition, community [32]byte, owner string) *Folded {
	byVsk := map[string]map[string][]*edition{}
	for _, e := range eds {
		m := byVsk[e.vsk]
		if m == nil {
			m = map[string][]*edition{}
			byVsk[e.vsk] = m
		}
		eid := hexOf(e.entity)
		m[eid] = append(m[eid], e)
	}
	candidatesOf := func(vsk string) map[string][]*edition {
		out := map[string][]*edition{}
		for eid, list := range byVsk[vsk] {
			out[eid] = candidates(list)
		}
		return out
	}
	heads := map[string]head{}

	// The roster: an entity's coordinate must be the role's own id or the
	// member's grant locator, so nothing can be filed under someone else.
	roleCands := map[string][]roleCand{}
	for eid, cs := range candidatesOf(vskRole) {
		for _, e := range cs {
			if r, ok := roleFrom(e.content); ok && r.ID == eid {
				roleCands[eid] = append(roleCands[eid], roleCand{r, e})
			}
		}
	}
	grantCands := map[string][]grantCand{}
	for eid, cs := range candidatesOf(vskGrant) {
		for _, e := range cs {
			g, ok := grantFrom(e.content)
			if !ok {
				continue
			}
			member, _ := hex32(g.Member)
			if hexOf(grantLocator(community, member)) == eid {
				grantCands[eid] = append(grantCands[eid], grantCand{g, e})
			}
		}
	}
	roster := delegate(roleCands, grantCands, community, owner, heads)
	cited := func(e *edition) bool { return citationSatisfied(heads, community, owner, e.author, e.authority) }
	rank := func(a string) float64 {
		if a == owner {
			return -1
		}
		if p, ok := roster.highest(a); ok {
			return p
		}
		return noRank
	}

	// Channels, each gated by MANAGE_CHANNELS. Deletion is terminal
	// (CORD-03 §2): it is decided over the whole accepted chain, so a later
	// edition can't lift it and split members who discarded the keys from
	// members who didn't.
	channels := map[string]Channel{}
	for eid, cs := range candidatesOf(vskChannel) {
		gate := func(e *edition) bool {
			if !roster.authorized(e.author, owner, PermManageChannels) || !cited(e) {
				return false
			}
			m, ok := channelMeta(e.content)
			return ok && m.Name != "" && len(m.Name) <= nameMaxBytes
		}
		h := pickHead(cs, heads, gate, rank)
		if h == nil {
			continue
		}
		m, _ := channelMeta(h.content)
		deleted := m.Deleted
		for _, e := range cs {
			if gate(e) {
				if x, _ := channelMeta(e.content); x.Deleted {
					deleted = true
				}
			}
		}
		channels[eid] = Channel{ID: eid, Name: m.Name, Private: m.Private, Deleted: deleted}
	}

	// The banlist, the one anti-roster. Holding BAN is not authority over
	// everyone: banning acts on a member, so it takes the bit and a strict
	// outrank, or a stock moderator could ban every admin and every client
	// would then drop the admins' editions too. The versions are walked in
	// order carrying the standing list: an author it already bans is not
	// admissible (else a banned moderator who still holds BAN publishes
	// the next version without themselves), and an edition only rewrites
	// the entries its author is entitled to, so it can't lift a ban its
	// author could never have issued.
	banned := map[string]bool{}
	{
		eid := hexOf(banlistLocator(community))
		cs := candidatesOf(vskBanlist)[eid]
		entitled := func(author, pk string) bool { return roster.canActOnMember(author, owner, pk, PermBan) }
		wellFormed := func(e *edition) bool {
			_, ok := banList(e.content)
			return roster.authorized(e.author, owner, PermBan) && cited(e) && ok
		}
		standingAt := map[*edition]map[string]bool{}
		standing := map[string]bool{}
		for _, group := range versionGroups(cs, func(e *edition) uint64 { return e.version }) {
			slices.SortStableFunc(group, func(a, b *edition) int {
				ra, rb := rank(a.author), rank(b.author)
				if ra != rb {
					if ra < rb {
						return -1
					}
					return 1
				}
				return strings.Compare(a.rumorHex, b.rumorHex)
			})
			for _, e := range group {
				if !wellFormed(e) || standing[e.author] {
					continue
				}
				list, _ := banList(e.content)
				named := map[string]bool{}
				for _, pk := range list {
					if entitled(e.author, pk) {
						named[pk] = true
					}
				}
				for pk := range standing {
					if !entitled(e.author, pk) {
						named[pk] = true
					}
				}
				standing = named
				standingAt[e] = named
				break
			}
		}
		if h := pickHead(cs, heads, func(e *edition) bool { _, ok := standingAt[e]; return ok }, rank); h != nil {
			for pk := range standingAt[h] {
				banned[pk] = true
			}
		}
	}
	return &Folded{Roster: roster, Owner: owner, Channels: channels, Banned: banned}
}

type channelMetadata struct {
	Name    string
	Private bool
	Deleted bool
}

// channelMeta reads a channel edition: name must be a string, and private
// and deleted count only when exactly true.
func channelMeta(content string) (channelMetadata, bool) {
	var w map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &w) != nil || w == nil {
		return channelMetadata{}, false
	}
	var m channelMetadata
	if json.Unmarshal(w["name"], &m.Name) != nil || !utf8.ValidString(m.Name) {
		return channelMetadata{}, false
	}
	m.Private = string(w["private"]) == "true"
	m.Deleted = string(w["deleted"]) == "true"
	return m, true
}

// banList reads a banlist edition: an array, of which only 64-hex strings
// count, lowercased.
func banList(content string) ([]string, bool) {
	var raw []json.RawMessage
	if json.Unmarshal([]byte(content), &raw) != nil || raw == nil {
		return nil, false
	}
	out := []string{}
	for _, r := range raw {
		var pk string
		if json.Unmarshal(r, &pk) == nil && isHex64(pk) {
			out = append(out, strings.ToLower(pk))
		}
	}
	return out, true
}

func hexOf(b [32]byte) string { return hex.EncodeToString(b[:]) }

func mapsKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

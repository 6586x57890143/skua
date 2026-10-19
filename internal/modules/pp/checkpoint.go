package pp

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

// The weekly checkpoint: when a server's week turns, the top places on each
// board (voice and chat) are paid Balance.Prizes from the treasury, which
// has first claim on it, before any grant. A treasury that can't pay every
// place splits what it has evenly between them, so a short week doesn't
// fall on whoever is paid last. Prizes go into the purse, not the level or
// the season: levels are time, and the season is its own race.

// prize is one place paid at a checkpoint.
type prize struct {
	user   snowflake.ID
	board  int
	place  int
	amount int64
}

// checkpointNews is a checkpoint for the herald to post.
type checkpointNews struct {
	week   int64
	prizes []prize
	short  bool // the treasury couldn't pay every place, so it split evenly
}

// checkpoint pays week's places. s.mu held.
func (st *state) checkpoint(s *server, week, today int64) {
	news := checkpointNews{week: week}
	var owed int64
	for board := range Boards {
		var users []snowflake.ID
		var scores []int64
		for u, m := range s.members {
			if m.week == week && m.weekAct[board] > 0 {
				users = append(users, u)
				scores = append(scores, m.weekAct[board])
			}
		}
		places := Places(scores)
		order := make([]int, len(users))
		for i := range order {
			order[i] = i
		}
		slices.SortFunc(order, func(a, b int) int {
			return cmp.Or(cmp.Compare(places[a], places[b]), cmp.Compare(users[a], users[b]))
		})
		for _, i := range order {
			want := st.bal().Prize(places[i])
			if want == 0 {
				break
			}
			owed += want
			news.prizes = append(news.prizes, prize{user: users[i], board: board, place: places[i], amount: want})
		}
	}
	if owed > s.treasury {
		news.short = true
		share := s.treasury / int64(len(news.prizes))
		for i := range news.prizes {
			news.prizes[i].amount = share
		}
		if share == 0 {
			news.prizes = nil
		}
	}
	for _, p := range news.prizes {
		m := s.members[p.user]
		s.treasury -= p.amount
		m.purse += p.amount
		s.append(0, today, reasonPrize, -p.amount, []int64{int64(p.user), int64(p.board), int64(p.place)})
		s.append(p.user, today, reasonPrize, p.amount, []int64{int64(p.board), int64(p.place)})
		m.dirty, s.dirty = true, true
	}
	if len(news.prizes) > 0 || news.short {
		s.checkpoints = append(s.checkpoints, news)
	}
}

// boardNames are the boards as members read them.
var boardNames = [Boards]string{"voice", "chat"}

// checkpointText is a checkpoint as the ledger channel shows it.
func checkpointText(n checkpointNews) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the week of %s is over, and the treasury pays its best", dayDate(n.week*7-3))
	for board := range Boards {
		first := true
		for _, p := range n.prizes {
			if p.board != board {
				continue
			}
			if first {
				fmt.Fprintf(&b, "\n**%s**", boardNames[board])
				first = false
			}
			fmt.Fprintf(&b, "\n%d · <@%s> +%s pp", p.place, p.user, grouped(p.amount/unit))
		}
	}
	if n.short {
		b.WriteString("\n-# the treasury ran short, so it split what it had evenly between the places")
	}
	return b.String()
}

// postCheckpoints posts what each server's checkpoints paid. It runs from
// the loop, after the save that wrote them.
func (m *Module) postCheckpoints() {
	for g, s := range m.st.servers() {
		s.mu.Lock()
		news, ready := s.checkpoints, len(s.pending) == 0
		if ready {
			s.checkpoints = nil
		}
		s.mu.Unlock()
		if !ready || !m.enabled(g) {
			continue
		}
		for _, n := range news {
			m.post(g, "checkpoint", checkpointText(n))
		}
	}
}

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
// has first claim on it, before any grant. A short treasury pays down the
// places until it's empty. Prizes go into the purse, not the level or the
// season: levels are time, and the season is its own race.

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
	short  bool // the treasury ran out before every place was paid
}

// checkpoint pays week's places. s.mu held.
func (st *state) checkpoint(s *server, week, today int64) {
	news := checkpointNews{week: week}
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
			want := st.b.Prize(places[i])
			if want == 0 {
				break
			}
			pay := min(want, s.treasury)
			if pay < want {
				news.short = true
			}
			if pay <= 0 {
				continue
			}
			u, m := users[i], s.members[users[i]]
			s.treasury -= pay
			m.purse += pay
			s.append(0, today, reasonPrize, -pay, []int64{int64(u), int64(board), int64(places[i])})
			s.append(u, today, reasonPrize, pay, []int64{int64(board), int64(places[i])})
			m.dirty, s.dirty = true, true
			news.prizes = append(news.prizes, prize{user: u, board: board, place: places[i], amount: pay})
		}
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
		b.WriteString("\n-# the treasury ran short, so it paid the places it could")
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

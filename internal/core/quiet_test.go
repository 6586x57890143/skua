package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
)

// body records what a REST call would have sent.
type body struct {
	rest.Client
	sent string
}

func (b *body) Do(_ *rest.CompiledEndpoint, rq, _ any, _ ...rest.RequestOpt) error {
	j, err := json.Marshal(rq)
	b.sent = string(j)
	return err
}

// An edit that sets no mentions, like a reminder's done button redrawing a
// card with <@someone> in its text, must still ping no one.
func TestQuietEditPingsNoOne(t *testing.T) {
	c := &body{}
	r := rest.New(c, Quiet())
	u := discord.NewMessageUpdateV2(discord.NewContainer(discord.NewTextDisplay("### <@175928847299117063> <@&1>")))
	if _, err := r.UpdateMessage(1, 2, u); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.sent, `"allowed_mentions":{"parse":[]`) {
		t.Fatalf("edit went out without parse:[]: %s", c.sent)
	}
	if _, err := r.CreateMessage(1, discord.MessageCreate{Content: "<@175928847299117063>"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.sent, `"allowed_mentions":{"parse":[]`) {
		t.Fatalf("send went out without parse:[]: %s", c.sent)
	}
}

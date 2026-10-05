package brand

import "github.com/disgoorg/disgo/discord"

// Card is a reply in UX.md's card form: one Components V2 container
// accented in color, its first line the module's emoji, its name and what
// this reply is, then body. Anything else it holds (a button row) comes in
// extra. It attaches nothing: before the emoji sync the head is text alone.
func Card(color int, module, what, body string, extra ...discord.ContainerSubComponent) discord.ContainerComponent {
	head := "**" + module + "**"
	if e := Mention("mod_" + module); e != "" {
		head = e + " " + head
	}
	if what != "" {
		head += " · " + what
	}
	parts := append([]discord.ContainerSubComponent{discord.NewTextDisplay(head + "\n" + body)}, extra...)
	return discord.NewContainer(parts...).WithAccentColor(color)
}

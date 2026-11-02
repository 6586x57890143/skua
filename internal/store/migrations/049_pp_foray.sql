-- Forays beyond the north gate (LORE.md): each member's day out, today's
-- or the last, and what going out has left them. The outing's columns are
-- what the story is walked from again (place, reach, lean, class, the
-- class arc's stage, the day and the fold bird met), never the story
-- itself; path is the events the day dealt, by key. pages and endings are
-- place/key, and arcs place/class=stages told, comma separated, so the book
-- survives a deck that grows or moves.
create table pp_foray (
	guild_id  bigint  not null,
	user_id   bigint  not null,
	last_out  bigint  not null default 0,
	peat      bigint  not null default 0,
	amber     bigint  not null default 0,
	amber_day bigint  not null default 0,
	out       boolean not null default false,
	place     bigint  not null default 0,
	lean      bigint  not null default 0,
	stage     bigint  not null default 0,
	quiet     bigint  not null default 0,
	need      bigint  not null default 0,
	day       bigint  not null default 0,
	companion bigint  not null default 0,
	far       boolean not null default false,
	back      boolean not null default false,
	class     text    not null default '',
	partner   text    not null default '',
	arcs      text    not null default '',
	pages     text    not null default '',
	endings   text    not null default '',
	path      text    not null default '',
	primary key (guild_id, user_id)
);

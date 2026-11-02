-- Forays beyond the north gate (LORE.md): each member's day out, today's
-- or the last, and what going out has left them. The outing's columns are
-- what the story is walked from again (place, reach, lean, class, the
-- class arc's stage, the day and the fold bird met), never the story
-- itself. pages and endings are a bitmask per place, in the order the gate
-- offers them; arcs is place/class=stages told, comma separated.
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
	pages     bigint[] not null default '{0,0}',
	endings   bigint[] not null default '{0,0}',
	primary key (guild_id, user_id)
);

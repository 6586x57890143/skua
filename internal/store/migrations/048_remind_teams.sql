-- remind's teams: a named list of members in a server that a reminder
-- can ping, joined and left from a card posted in its channel. card_id is
-- that card, 0 until it is posted. remind_here is who answered here to
-- one turn of a group reminder, for its tally.
create table if not exists remind_team (
	id         bigserial primary key,
	guild_id   bigint not null,
	name       text not null,
	channel_id bigint not null,
	created_by bigint not null,
	card_id    bigint not null default 0,
	created_at timestamptz not null default now()
);
create unique index if not exists remind_team_name on remind_team (guild_id, lower(name));

create table if not exists remind_team_member (
	team_id bigint not null references remind_team (id) on delete cascade,
	user_id bigint not null,
	primary key (team_id, user_id)
);

create table if not exists remind_here (
	remind_id bigint not null,
	due_at    timestamptz not null,
	user_id   bigint not null,
	primary key (remind_id, due_at, user_id)
);

-- The counting game. Each counting channel's whole state as the engine
-- keeps it, written behind a few times a second.
create table count_channel (
	channel_id bigint primary key,
	guild_id   bigint not null,
	state      jsonb  not null
);
create index count_channel_guild on count_channel (guild_id);

-- Each member's counting in a server: the columns a board sorts by, and the
-- rest as the engine keeps it.
create table count_member (
	guild_id bigint not null,
	user_id  bigint not null,
	counts   bigint not null,
	xp       bigint not null,
	state    jsonb  not null,
	primary key (guild_id, user_id)
);

-- Counts per member per calendar month (UTC), for the season board.
create table count_season (
	guild_id bigint not null,
	period   text   not null,
	user_id  bigint not null,
	counts   bigint not null,
	primary key (guild_id, period, user_id)
);

-- Every accepted count: the rows a run's chain is made of.
create table count_log (
	channel_id bigint not null,
	run        bigint not null,
	n          bigint not null,
	user_id    bigint not null,
	message_id bigint not null,
	primary key (channel_id, run, n)
);

-- Every run that ended: where it began, how far it went and its head, so a
-- result can be checked against the log.
create table count_run (
	channel_id bigint      not null,
	run        bigint      not null,
	guild_id   bigint      not null,
	start      bigint      not null,
	length     bigint      not null,
	head       text        not null,
	broke_by   bigint      not null,
	ended      timestamptz not null,
	primary key (channel_id, run)
);

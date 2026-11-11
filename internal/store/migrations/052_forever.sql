-- forever: a server's own WoW Forever ladder. A character is kept once,
-- however many servers list it, keyed by its region and Blizzard's id there;
-- reached is when it reached its level, which breaks a tie on the ladder.
create table if not exists forever_char (
	region   text not null,
	id       bigint not null,
	name     text not null,
	surname  text not null,
	class    text not null default '',
	class_id int not null default 0,
	race     text not null default '',
	ruleset  text not null default '',
	faction  text not null default '',
	level    int not null default 0,
	xp       int not null default 0,
	points   int not null default 0,
	ilvl     int not null default 0,
	avatar   text not null default '',
	render   text not null default '',
	modified text not null default '',
	reached  timestamptz not null default now(),
	primary key (region, id)
);

-- A character listed in a server by the member who plays it. verified is a
-- battle.net link's word that it is theirs.
create table if not exists forever_claim (
	guild_id bigint not null,
	user_id  bigint not null,
	region   text not null,
	char_id  bigint not null,
	verified boolean not null default false,
	added_at timestamptz not null default now(),
	primary key (guild_id, region, char_id)
);
create index if not exists forever_claim_user on forever_claim (guild_id, user_id);

-- Every level a character was seen to reach and when, for the recap and the
-- challenges: at is when skua saw it, a poll after the logout.
create table if not exists forever_ding (
	region  text not null,
	char_id bigint not null,
	level   int not null,
	at      timestamptz not null,
	primary key (region, char_id, level)
);

-- Where a server's feed goes and the role its members pick.
create table if not exists forever_guild (
	guild_id bigint primary key,
	feed_id  bigint not null default 0,
	role_id  bigint not null default 0
);

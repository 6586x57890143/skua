-- /pp: a member's standing in one server and the day they are in, saved
-- every minute so a deploy loses at most the minute not yet written.
-- Amounts are hundredths of a pp. total is everything earned, which sets the
-- level and never falls; purse is what is left to spend. day, week and
-- season are numbers: days since 1970 (UTC), weeks since then from a
-- Monday, seasons from 1. class_changes is the changes paid for in
-- class_season.
create table pp_member (
	guild_id     bigint not null,
	user_id      bigint not null,
	total        bigint not null default 0,
	purse        bigint not null default 0,
	season       bigint not null default 0,
	season_pp    bigint not null default 0,
	week         bigint not null default 0,
	week_voice   bigint not null default 0,
	week_chat    bigint not null default 0,
	day          bigint not null default 0,
	day_voice    bigint not null default 0,
	day_chat     bigint not null default 0,
	day_talk     bigint not null default 0,
	rested       bigint not null default 0,
	class        text   not null default 'wanderer',
	class_season bigint not null default -1,
	class_changes bigint not null default 0,
	voice_min    bigint not null default 0,
	messages     bigint not null default 0,
	primary key (guild_id, user_id)
);

-- The server's own day, which lights the hearth: what it has counted today,
-- the days before (newest last, up to a week), and the tier it reached
-- today and the day before.
create table pp_guild (
	guild_id   bigint   primary key,
	day        bigint   not null default 0,
	raw        bigint   not null default 0,
	history    bigint[] not null default '{}',
	lit        int      not null default 0,
	lit_before int      not null default 0
);

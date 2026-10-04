-- purge: one row per member who has asked skua to delete their messages in a
-- guild. Snowflakes fit a bigint: Discord's top bit is never set.
create table purge_subs (
	guild_id         bigint not null,
	user_id          bigint not null,
	-- null is off.
	live_delay_s     int,
	every_s          int,
	next_run         timestamptz,
	-- Every message of theirs up to this ID is gone; a scheduled sweep
	-- starts reading here.
	swept_through    bigint not null default 0,
	last_deleted     int,
	last_unreachable int,
	last_finished    timestamptz,
	primary key (guild_id, user_id)
);

create index purge_subs_next_run on purge_subs (next_run) where next_run is not null;

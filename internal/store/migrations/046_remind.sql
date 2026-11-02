-- remind: what members asked to be reminded of, and how each wants it.
-- A row is pending until it fires; a one-off then rests as done for a
-- week, so its card's snooze still finds it, and a repeat moves on to its
-- next due_at. sent_at is when a send was claimed, so only a claim
-- left far behind (a process that died mid send) is taken back. target is dm or channel (target_id unused); mask, mday and
-- at_min are the repeat rule, all zero for a one-off.
create table if not exists remind (
	id         bigserial primary key,
	guild_id   bigint not null,
	owner_id   bigint not null,
	channel_id bigint not null,
	target     text not null default 'dm',
	target_id  bigint not null default 0,
	text       text not null,
	due_at     timestamptz not null,
	mask       smallint not null default 0,
	mday       smallint not null default 0,
	at_min     smallint not null default 0,
	tz         text not null default 'UTC',
	state      text not null default 'pending',
	sent_at    timestamptz,
	created_at timestamptz not null default now()
);
create index if not exists remind_due on remind (due_at) where state = 'pending';
create index if not exists remind_owner on remind (owner_id);

create table if not exists remind_user (
	user_id    bigint primary key,
	tz         text not null default '',
	dm_channel bigint not null default 0
);

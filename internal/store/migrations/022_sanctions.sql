-- Every sentence internal/sanction handed down, written before it was
-- applied, so the ladder reads a member's record across rules and restarts.
create table sanctions (
	guild_id bigint      not null,
	user_id  bigint      not null,
	rule     text        not null,
	at       timestamptz not null,
	lasts    interval    not null
);
create index sanctions_member on sanctions (guild_id, user_id, at);

-- The channels the spam module watches in a guild; channel 0 is all of them.
create table spam_scope (
	guild_id   bigint not null,
	channel_id bigint not null,
	primary key (guild_id, channel_id)
);

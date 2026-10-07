-- A live card skua posted, kept until its stream ends, when the card is
-- edited into the stream's VOD. item is the stream as the card showed it.
create table notify_live (
	message_id bigint      primary key,
	platform   text        not null,
	account    text        not null,
	live_id    text        not null,
	guild_id   bigint      not null,
	channel_id bigint      not null,
	role_id    bigint      not null default 0,
	item       jsonb       not null,
	posted_at  timestamptz not null
);
create index notify_live_stream on notify_live (platform, account, live_id);

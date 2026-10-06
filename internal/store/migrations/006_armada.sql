-- Which Discord message is which Armada rumor, so replies, deletes and
-- echoes resolve across the bridge. Ids and authors only, never content.
-- origin is the side the message was written on; author is a Discord user
-- id for 'discord' and a pubkey for 'armada'. A long rumor becomes several
-- Discord messages, numbered by part.
create table armada_messages (
	discord_message_id bigint not null,
	discord_channel_id bigint not null,
	webhook_id         bigint,
	rumor_id           text   not null,
	origin             text   not null check (origin in ('discord', 'armada')),
	author             text   not null,
	part               int    not null default 0,
	primary key (discord_message_id, rumor_id, part)
);
create index armada_messages_rumor on armada_messages (rumor_id, discord_channel_id);

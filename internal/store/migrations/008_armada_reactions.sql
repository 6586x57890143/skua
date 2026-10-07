-- Which kind 7 is which Discord reaction, so a removal on either side finds
-- the reaction it takes back. emoji is as Discord's reaction routes take it:
-- the unicode itself, or name:id. Every Armada reactor of one emoji on one
-- message shares skua's single reaction there, which comes off with the last.
create table armada_reactions (
	rumor_id           text   primary key,
	discord_channel_id bigint not null,
	discord_message_id bigint not null,
	emoji              text   not null,
	origin             text   not null check (origin in ('discord', 'armada')),
	author             text   not null
);
create index armada_reactions_message on armada_reactions (discord_message_id, emoji);

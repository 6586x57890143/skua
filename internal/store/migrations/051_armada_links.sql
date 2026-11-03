-- Which Discord channel carries which Armada channel, as an admin picked
-- it on armada's /help page. Each side is in one pair at most. Pairs from
-- SKUA_ARMADA_LINKS are not kept here.
create table armada_links (
	discord_channel_id bigint primary key,
	guild_id           bigint not null,
	armada_channel     text   not null unique
);

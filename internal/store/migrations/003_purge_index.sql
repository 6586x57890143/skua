-- purge: the index. Each channel and thread is read once, ever; a purge
-- catches up from read_through and looks its member up here instead of
-- reading the server again. IDs and authors only, never content.
create table purge_channels (
	guild_id          bigint not null,
	channel_id        bigint not null,
	-- Every message up to this ID has been read into purge_postings.
	read_through      bigint not null default 0,
	-- When this channel's archived threads were last all listed: a later
	-- listing stops at threads archived before it.
	threads_listed_at timestamptz,
	primary key (guild_id, channel_id)
);

-- One author's message IDs in one channel, packed (purge/postings.go): the
-- block's first ID, then a millisecond gap and the low 22 bits for each
-- next one. About 6 bytes a message.
create table purge_postings (
	guild_id   bigint not null,
	author_id  bigint not null,
	channel_id bigint not null,
	first_id   bigint not null,
	ids        bytea  not null,
	primary key (guild_id, author_id, channel_id, first_id)
);

-- The blocks are already packed: compressing them again costs CPU on every
-- write and saves nothing.
alter table purge_postings alter column ids set storage external;

-- The index's marks replace each member's own place.
alter table purge_subs drop column swept_through;

-- Previews fix left beside its card, per server, day (unix days) and why,
-- so a bug hunt still has the counts after a restart. Rows older than 30
-- days are dropped at boot.
create table fix_kept (
	guild_id bigint not null,
	day      bigint not null,
	reason   text   not null,
	n        bigint not null default 0,
	primary key (guild_id, day, reason)
);

-- Counts of what members do that the ledger doesn't hold, a day at a time,
-- for tuning: quests dealt, rerolled and finished by tier and kind, and
-- notices posted, finished and lapsed by kind. Read only as aggregates.
create table pp_stat (
	guild_id bigint not null,
	day      bigint not null,
	what     text   not null,
	a        bigint not null,
	b        bigint not null,
	n        bigint not null,
	primary key (guild_id, day, what, a, b)
);

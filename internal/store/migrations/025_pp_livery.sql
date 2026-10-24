-- Liveries: the role every livery skua makes sits directly under, and the
-- channel petitions for a livery of one's own are posted in for review.
alter table pp_guild
	add column livery_anchor   bigint not null default 0,
	add column heralds_channel bigint not null default 0;

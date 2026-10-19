-- The contest fund, the part of the treasury only grants spend, and
-- whether it has had its one seed. levy_since, grant_week and
-- grant_week_sum are no longer read; they stay so an older build still
-- loads.
alter table pp_guild
	add column fund        bigint  not null default 0,
	add column fund_seeded boolean not null default false;

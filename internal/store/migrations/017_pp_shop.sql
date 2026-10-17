-- /pp's shop. A member's prestige rung, and what their open day earned past
-- the heavy purse line and so was never minted. A server's season so far:
-- what it minted and burned, the last season's burn in percent of what it
-- minted (-1 before there was one), and whether the heavy purse line is on.
alter table pp_member
	add column rung          bigint not null default 0,
	add column day_withheld  bigint not null default 0;

alter table pp_guild
	add column season_mint bigint  not null default 0,
	add column season_burn bigint  not null default 0,
	add column sink_last   bigint  not null default -1,
	add column heavy       boolean not null default false;
